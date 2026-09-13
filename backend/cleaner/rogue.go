package cleaner

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
	"drivewise/backend/rules"
)

// RogueService 流氓软件清理服务，作为 Wails v3 服务暴露给前端
type RogueService struct{}

// BackupEntry 清理备份记录（用于恢复）
type BackupEntry struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RuleType     string `json:"ruleType"`
	RiskLevel    string `json:"riskLevel"`
	Time         string `json:"time"`
	RegHive      string `json:"regHive,omitempty"`  // HKLM | HKCU | HKCR
	RegKey       string `json:"regKey,omitempty"`   // 注册表键路径
	RegValue     string `json:"regValue,omitempty"` // 值名（"" 表示键的默认值）
	RegData      string `json:"regData,omitempty"`  // 原值数据（字符串/数字的可读形式）
	RegKind      uint32 `json:"regKind,omitempty"`  // 原值类型（REG_*，0=旧版备份仅字符串）
	RegBlob      []byte `json:"regBlob,omitempty"`  // 原值原始字节（按类型原样恢复）
	RegTree      string `json:"regTree,omitempty"`  // 整棵子树的 JSON 备份（含所有值与子键，用于「删整个键」类清理的原样还原）
	OrigPath     string `json:"origPath,omitempty"`   // 原文件/目录路径
	BackedPath   string `json:"backedPath,omitempty"` // 备份后的路径
	ServiceName  string `json:"serviceName,omitempty"` // 服务名
	ServiceStart uint32 `json:"serviceStart,omitempty"` // 服务原启动类型
	TaskName     string `json:"taskName,omitempty"`    // 计划任务名
	TaskXML      string `json:"taskXML,omitempty"`     // 计划任务备份 XML
}

// regTarget 注册表扫描目标
type regTarget struct {
	hive registry.Key
	path string
	desc string
}

var runKeys = []regTarget{
	{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Run`, "HKLM 启动项"},
	{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\RunOnce`, "HKLM 一次性启动项"},
	{registry.LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Run`, "HKLM 32位启动项"},
	{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, "HKCU 启动项"},
	{registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\RunOnce`, "HKCU 一次性启动项"},
}

// ScanRogue 扫描流氓/推广软件组件
// 覆盖：启动项/启动文件夹/服务/计划任务/浏览器扩展/已装软件/黑名单目录/
//      运行中进程/Shell 扩展注入点/签名黑名单/系统启动劫持点/
//      浏览器主页与搜索劫持/隐蔽自启点(Userinit 等)/Hosts 劫持/快捷方式参数注入/
//      外壳命名空间扩展（此电脑「设备与驱动器」图标、驱动器右键菜单残留）
func (s *RogueService) ScanRogue() []models.RogueItem {
	items := make([]models.RogueItem, 0, 16)
	items = append(items, scanRunKeys()...)
	items = append(items, scanStartupFolders()...)
	items = append(items, scanServices()...)
	items = append(items, scanTasks()...)
	items = append(items, scanExtensions()...)
	items = append(items, scanInstalled()...)
	items = append(items, scanBlackDirs()...)
	items = append(items, scanProcesses()...)
	items = append(items, scanShellEx()...)
	items = append(items, scanSignedFiles()...)
	items = append(items, scanHijackPoints()...)
	items = append(items, scanExtraAutorun()...)
	items = append(items, scanBrowserHijacks()...)
	items = append(items, scanHosts()...)
	items = append(items, scanShortcuts()...)
	items = append(items, scanNamespaceExt()...)
	return dedupeItems(items)
}

// dedupeItems 按 ID 保序去重（不同维度可能命中同一条目）
func dedupeItems(items []models.RogueItem) []models.RogueItem {
	seen := make(map[string]bool, len(items))
	out := items[:0]
	for _, it := range items {
		if it.ID == "" || seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		out = append(out, it)
	}
	return out
}

// ============ 各维度扫描 ============

// scanRunKeys 扫描注册表启动项
func scanRunKeys() []models.RogueItem {
	var items []models.RogueItem
	for _, t := range runKeys {
		hivePrefix := "HKLM"
		if t.hive == registry.CURRENT_USER {
			hivePrefix = "HKCU"
		}
		k, err := registry.OpenKey(t.hive, t.path, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, name := range names {
			data, _, err := k.GetStringValue(name)
			if err != nil {
				continue
			}
			if rule := matchRule(name + " " + data); rule != nil {
				items = append(items, rogueItem("registry", rule, t.desc,
					hivePrefix+`\`+t.path+`\`+name, data, rule.Action))
			}
		}
		k.Close()
	}
	return items
}

// scanStartupFolders 扫描启动文件夹
func scanStartupFolders() []models.RogueItem {
	var items []models.RogueItem
	dirs := []string{
		`C:\ProgramData\Microsoft\Windows\Start Menu\Programs\StartUp`,
		filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\Startup`),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if rule := matchRule(e.Name()); rule != nil {
				items = append(items, rogueItem("startup", rule, "启动文件夹", full, e.Name(), "remove"))
			}
		}
	}
	return items
}

// scanServices 扫描 Windows 服务（按服务名与映像路径匹配）
func scanServices() []models.RogueItem {
	var items []models.RogueItem
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	subs, _ := k.ReadSubKeyNames(-1)
	for _, sub := range subs {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+sub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		display, _, _ := sk.GetStringValue("DisplayName")
		img, _, _ := sk.GetStringValue("ImagePath")
		svcType, _, _ := sk.GetIntegerValue("Type")
		start, _, _ := sk.GetIntegerValue("Start")
		sk.Close()
		if rule := matchService(sub, display+" "+img); rule != nil {
			action := rule.Action
			if action == "remove" {
				action = "disable_service" // 服务统一采用禁用方式，避免误删系统组件
			}
			item := rogueItem("service", rule, "Windows 服务", sub, display, action)
			// 驱动/开机启动型服务禁用后需重启生效，明确告知用户
			if svcType == 1 || start <= 1 {
				item.Detail += "（内核驱动/启动型服务，禁用后需重启系统完全生效）"
			}
			items = append(items, item)
		}
	}
	k.Close()
	return items
}

// scanTasks 扫描计划任务（schtasks 列表，匹配任务名）
// 注意：中文 Windows 上 schtasks 输出为 GBK，任务名含中文时必须解码，否则乱码无法匹配
func scanTasks() []models.RogueItem {
	var items []models.RogueItem
	out, err := runHiddenOutput("schtasks", "/query", "/fo", "CSV", "/nh")
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(decodeDismOutput(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// CSV 首字段为任务名（可能带引号与逗号）
		name := line
		if idx := strings.Index(line, `","`); idx > 0 {
			name = line[1:idx]
		} else if idx := strings.IndexByte(line, ','); idx > 0 {
			name = line[:idx]
		}
		name = strings.Trim(name, `"`)
		if name == "" || strings.HasPrefix(name, `\Microsoft\`) {
			continue // 跳过系统内置任务
		}
		if rule := matchTask(name); rule != nil {
			action := rule.Action
			if action == "remove" {
				action = "disable_task"
			}
			items = append(items, rogueItem("task", rule, "计划任务", name, "", action))
		}
	}
	return items
}

// scanExtensions 扫描浏览器强制安装扩展（策略注册表）
func scanExtensions() []models.RogueItem {
	var items []models.RogueItem
	policies := []regTarget{
		{registry.LOCAL_MACHINE, `SOFTWARE\Policies\Google\Chrome\ExtensionInstallForcelist`, "Chrome 强制扩展"},
		{registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Edge\ExtensionInstallForcelist`, "Edge 强制扩展"},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Policies\Google\Chrome\ExtensionInstallForcelist`, "Chrome 强制扩展(32位)"},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Policies\Microsoft\Edge\ExtensionInstallForcelist`, "Edge 强制扩展(32位)"},
	}
	for _, t := range policies {
		k, err := registry.OpenKey(t.hive, t.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, name := range names {
			data, _, err := k.GetStringValue(name)
			if err != nil {
				continue
			}
			if rule := matchExtension(data); rule != nil {
				items = append(items, rogueItem("extension", rule, t.desc,
					"HKLM\\"+t.path, data, "remove_extension"))
			}
		}
		k.Close()
	}
	return items
}

// scanInstalled 扫描已安装软件（显示名/发布者/安装目录匹配 + 签名黑名单）
func scanInstalled() []models.RogueItem {
	var items []models.RogueItem
	uninstallKeys := []regTarget{
		{registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, "已安装软件"},
		{registry.LOCAL_MACHINE, `Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`, "已安装软件(32位)"},
	}
	for _, t := range uninstallKeys {
		k, err := registry.OpenKey(t.hive, t.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		subs, _ := k.ReadSubKeyNames(-1)
		for _, sub := range subs {
			sk, err := registry.OpenKey(t.hive, t.path+`\`+sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			display, _, _ := sk.GetStringValue("DisplayName")
			pub, _, _ := sk.GetStringValue("Publisher")
			loc, _, _ := sk.GetStringValue("InstallLocation")
			sk.Close()
			if display == "" && pub == "" {
				continue
			}
			// 常规规则匹配
			if rule := matchRule(display + " " + pub + " " + loc); rule != nil {
				items = append(items, rogueItem("installed", rule, t.desc, sub, display, "hint"))
				continue
			}
			// softcnkiller 签名黑名单（发布者命中）
			if pub != "" && rules.IsBlacklistedPublisher(pub) {
				items = append(items, models.RogueItem{
					ID:        genID("sign", sub, pub),
					Name:      display,
					RuleType:  "installed",
					Location:  t.desc + "（发布者命中签名黑名单）",
					Path:      sub,
					Value:     pub,
					RiskLevel: "medium",
					Detail:    "发布者 " + pub + " 在 softcnkiller 签名黑名单中",
					Impact:    "发布者为已知流氓软件厂商，可能存在弹窗/捆绑/广告行为",
					Action:    "hint",
					Selected:  false,
				})
			}
		}
		k.Close()
	}
	return items
}

// scanBlackDirs 扫描常见安装目录（递归 ≤3 层），匹配 softcnkiller 目录/路径黑名单。
// 相比单层扫描，能发现嵌套安装的组件（如 %LOCALAPPDATA%\xxx\rogueDir）；
// 命中即整目录处理并剪枝（不再深入子目录），同时对遍历规模做预算保护。
func scanBlackDirs() []models.RogueItem {
	var items []models.RogueItem
	roots := []string{
		`C:\Program Files`,
		`C:\Program Files (x86)`,
		filepath.Join(os.Getenv("LOCALAPPDATA")),
		filepath.Join(os.Getenv("APPDATA")),
	}
	budget := 30000
	for _, root := range roots {
		if root == "" {
			continue
		}
		matches := collectDirMatches(root, 3, &budget,
			func(name, full string) bool {
				return rules.IsBlacklistedDir(name) || rules.IsBlacklistedPath(full)
			},
			func(name, full string) bool {
				// 系统/通用保留目录与白名单目录整体跳过（不匹配也不深入）
				return systemDirName(name) || rules.IsWhitelistedPath(full)
			})
		for _, full := range matches {
			name := filepath.Base(full)
			items = append(items, models.RogueItem{
				ID:        genID("dir", full, ""),
				Name:      name,
				RuleType:  "dir",
				Location:  "安装目录（softcnkiller 黑名单，深扫）",
				Path:      full,
				Value:     name,
				RiskLevel: "medium",
				Detail:    "目录名命中流氓软件黑名单",
				Impact:    "已知流氓软件安装目录，可能存在捆绑/弹窗/广告行为",
				Action:    "remove", // 备份后移入隔离区（backupAndMoveFile）
				Selected:  false,
			})
		}
	}
	return items
}

// collectDirMatches 在 root 下递归收集命中 isMatch 的目录（≤maxDepth 层，
// root 的直接子目录为第 1 层）。isPrune 命中时跳过该目录（不匹配也不深入）；
// 命中 isMatch 的目录同样不再深入（整目录处理）。budget 为全局遍历预算，
// 耗尽即停止，避免大型 AppData 拖慢扫描。
func collectDirMatches(root string, maxDepth int, budget *int, isMatch, isPrune func(name, full string) bool) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if *budget <= 0 {
			return
		}
		*budget--
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue // 隐藏目录
			}
			full := filepath.Join(dir, name)
			if isPrune(name, full) {
				continue
			}
			childDepth := depth + 1
			if isMatch(name, full) {
				out = append(out, full)
				continue
			}
			if childDepth < maxDepth {
				walk(full, childDepth)
			}
		}
	}
	walk(root, 0)
	return out
}

// systemDirName 系统/通用保留目录名（不参与黑名单匹配）
func systemDirName(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "temp", "tmp", "microsoft", "packages", "application data", "history",
		"temporary internet files", "google", "common files", "windows",
		"program files", "program files (x86)", "users", "public", "programdata",
		"system volume information", "recovery", "$recycle.bin", "perflogs",
		"inetpub", "msbuild", "node_modules", "amdcache", "nvidia corporation":
		return true
	}
	return false
}

// rogueItem 构造扫描结果项
func rogueItem(ruleType string, rule *rules.RogueRule, location, path, value, action string) models.RogueItem {
	return models.RogueItem{
		ID:        genID(ruleType, path, value),
		Name:      rule.Name,
		RuleType:  ruleType,
		Location:  location,
		Path:      path,
		Value:     value,
		RiskLevel: rule.RiskLevel,
		Detail:    rule.Note,
		Impact:    rule.Impact,
		Action:    action,
		Selected:  false, // 参考 RogueCleaner：默认不勾选，用户确认后清理
	}
}

// ============ 规则匹配 ============

// matchRule 通用关键词匹配（注册表值/路径/显示名等）
func matchRule(text string) *rules.RogueRule {
	lower := strings.ToLower(text)
	for i := range rules.RogueRules {
		r := &rules.RogueRules[i]
		for _, kw := range r.Keywords {
			if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
				return r
			}
		}
	}
	return nil
}

// matchService 服务维度匹配（服务名或映像路径）
func matchService(name, image string) *rules.RogueRule {
	lower := strings.ToLower(name + " " + image)
	for i := range rules.RogueRules {
		r := &rules.RogueRules[i]
		for _, kw := range r.Services {
			if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
				return r
			}
		}
	}
	return nil
}

// matchTask 计划任务维度匹配
func matchTask(name string) *rules.RogueRule {
	lower := strings.ToLower(name)
	for i := range rules.RogueRules {
		r := &rules.RogueRules[i]
		for _, kw := range r.Tasks {
			if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
				return r
			}
		}
	}
	return nil
}

// matchExtension 浏览器扩展维度匹配
func matchExtension(data string) *rules.RogueRule {
	lower := strings.ToLower(data)
	for i := range rules.RogueRules {
		r := &rules.RogueRules[i]
		for _, kw := range r.ExtIDs {
			if kw != "" && strings.Contains(lower, strings.ToLower(kw)) {
				return r
			}
		}
	}
	return nil
}

// ============ 清理与恢复 ============

// CleanRogue 清理选中项（按 ID，兼容旧调用）。
// 说明：先重新扫描再按 ID 匹配会引入“进程退出/目录消失 → ID 失配 → 找不到对应项目”
// 与全量重扫耗时问题；新调用方请优先使用 CleanRogueItems（直接传扫描结果对象）。
func (s *RogueService) CleanRogue(ids []string) []models.RogueCleanResult {
	results := make([]models.RogueCleanResult, 0, len(ids))
	manifest, _ := loadManifest()
	items := s.ScanRogue()
	byID := make(map[string]*models.RogueItem, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}

	for _, id := range ids {
		item := byID[id]
		if item == nil {
			results = append(results, models.RogueCleanResult{ID: id, OK: false, ErrMsg: "未找到对应项目（可能已被移除或进程已退出），请重新扫描后再试"})
			continue
		}
		results = append(results, cleanRogueItem(item, &manifest))
	}

	_ = saveManifest(manifest)
	return results
}

// CleanRogueItems 按扫描结果对象直接清理选中项（推荐）。
// 相比 CleanRogue(ids)：
//  1. 不再依赖清理前的全量重扫，速度快且不受“进程重启/目录消失导致 ID 失配”影响；
//  2. 单项失败原因精确返回（权限、占用、黑名单不符等），便于 UI 逐条提示。
func (s *RogueService) CleanRogueItems(items []models.RogueItem) []models.RogueCleanResult {
	results := make([]models.RogueCleanResult, 0, len(items))
	manifest, _ := loadManifest()
	for i := range items {
		results = append(results, cleanRogueItem(&items[i], &manifest))
	}
	_ = saveManifest(manifest)
	return results
}

// cleanRogueItem 执行单条清理并记录备份（不落盘 manifest，由调用方统一保存）。
func cleanRogueItem(item *models.RogueItem, manifest *[]BackupEntry) models.RogueCleanResult {
	// 仅提示型：不做一键清理（已装软件需官方卸载、Shell 扩展需在系统层处理），明确告知
	if item.Action == "hint" {
		return models.RogueCleanResult{ID: item.ID, OK: false, ErrMsg: "该项目仅提示，请在系统设置中自行处理: " + item.Value}
	}
	// 运行中进程：结束进程即为清理动作（无备份）；需留意其自启动项需另行清理
	if item.RuleType == "process" {
		if killed := killProcessesForItem(item); len(killed) > 0 {
			return models.RogueCleanResult{ID: item.ID, OK: true, ErrMsg: "已终止进程: " + strings.Join(killed, ", ")}
		}
		return models.RogueCleanResult{ID: item.ID, OK: false, ErrMsg: "未找到可结束的相关进程（可能已退出或需要管理员权限），请重新扫描确认"}
	}
	// 目录类清理前先结束目录内运行的进程（解除文件占用，否则整体迁移会失败）
	if item.RuleType == "dir" && item.Path != "" {
		if killed := killProcessesUnderDir(item.Path); len(killed) > 0 {
			_ = killed // 辅助动作，不单独计结果；错误也不阻塞后续尝试
		}
	}
	// 其它类型：清理前先结束相关进程（对抗进程注入/文件占用；辅助动作，不单独计结果）。
	// hosts/快捷方式/命名空间入口与运行进程无关联，跳过避免按显示名关键词误杀无关进程。
	if item.RuleType != "hosts" && item.RuleType != "shortcut" && item.RuleType != "namespace" {
		killProcessesForItem(item)
	}

	// 目录/文件/启动项目标必须与黑名单匹配（防越权把关键目录移入隔离区）
	if ok, why := rogueCleanTargetValid(item); !ok {
		return models.RogueCleanResult{ID: item.ID, OK: false, ErrMsg: why}
	}

	entry, err := backupAndClean(item)
	if err != nil {
		return models.RogueCleanResult{ID: item.ID, OK: false, ErrMsg: err.Error()}
	}
	if entry != nil {
		*manifest = append(*manifest, *entry)
	}
	return models.RogueCleanResult{ID: item.ID, OK: true}
}

// rogueCleanTargetValid 校验 dir/file/startup 类清理目标确实来自黑名单匹配，
// 防止外部构造的越权调用把系统/用户关键目录移入隔离区造成误伤。
func rogueCleanTargetValid(item *models.RogueItem) (bool, string) {
	// 命名空间/驱动器菜单入口：只允许操作已知扫描根键下的子键（先于类型分流校验）
	if item.RuleType == "namespace" {
		if !isKnownNamespacePath(item.Path) {
			return false, "目标不在已知的命名空间/驱动器菜单范围内，已拒绝"
		}
		return true, ""
	}
	if item.RuleType != "dir" && item.RuleType != "file" && item.RuleType != "startup" {
		return true, ""
	}
	if item.Path == "" {
		return false, "缺少目标路径，已拒绝"
	}
	abs := filepath.Clean(item.Path)
	vol := filepath.VolumeName(abs)
	// 盘根与系统关键目录本身一律拒绝
	if strings.EqualFold(abs, vol+`\`) {
		return false, "禁止处理磁盘根目录"
	}
	protected := []string{`\windows`, `\program files`, `\program files (x86)`,
		`\programdata`, `\users`, `\$recycle.bin`, `\system volume information`,
		`\recovery`, `\perflogs`}
	for _, p := range protected {
		if strings.EqualFold(abs, vol+p) {
			return false, "禁止处理系统关键目录: " + vol + p
		}
	}
	if rules.IsWhitelistedPath(abs) {
		return false, "目标在白名单中，已拒绝"
	}
	// 类型化校验：黑名单目录名 / 黑名单完整路径 / 启动文件夹命中
	switch item.RuleType {
	case "dir":
		if !rules.IsBlacklistedDir(filepath.Base(abs)) && !rules.IsBlacklistedPath(abs) {
			return false, "目录不在黑名单范围内，已拒绝（可能是残留误报，请先手动确认）"
		}
	case "file":
		if !rules.IsBlacklistedPath(abs) {
			return false, "文件不在黑名单范围内，已拒绝"
		}
	case "startup":
		parent := strings.ToLower(filepath.Dir(abs))
		if !strings.Contains(parent, `start menu\programs\startup`) {
			return false, "启动项不在启动文件夹范围内，已拒绝"
		}
	}
	return true, ""
}

// killProcessesForItem 清理前终止相关进程（按条目名称/路径提取关键词）
func killProcessesForItem(item *models.RogueItem) []string {
	var keywords []string
	if item.PID > 0 {
		// 直接按 PID 终止
		if err := terminateProcessByPID(uint32(item.PID)); err == nil {
			return []string{item.Value}
		}
	}
	// 按名称/路径关键词
	if item.Value != "" {
		keywords = append(keywords, item.Value)
	}
	if item.Name != "" {
		keywords = append(keywords, item.Name)
	}
	var killed []string
	seen := map[string]bool{}
	for _, kw := range keywords {
		if kw == "" || len(kw) < 3 {
			continue
		}
		for _, k := range killProcessesByKeyword(kw) {
			if !seen[k] {
				seen[k] = true
				killed = append(killed, k)
			}
		}
	}
	return killed
}

// RestoreRogue 按 ID 恢复已清理项
func (s *RogueService) RestoreRogue(ids []string) []models.RogueCleanResult {
	results := make([]models.RogueCleanResult, 0, len(ids))
	manifest, _ := loadManifest()

	for _, id := range ids {
		ok, rerr := restoreEntry(id, &manifest)
		msg := ""
		if rerr != nil {
			msg = rerr.Error()
		}
		results = append(results, models.RogueCleanResult{ID: id, OK: ok, ErrMsg: msg})
	}
	_ = saveManifest(manifest)
	return results
}

// GetBackups 列出所有可恢复的清理备份
func (s *RogueService) GetBackups() []BackupEntry {
	m, _ := loadManifest()
	return m
}

// GetBlacklistInfo 返回当前黑名单状态（条目数/更新时间/本地目录）
func (s *RogueService) GetBlacklistInfo() models.BlacklistInfo {
	dirs, paths, signs, white, localTime := rules.Info()
	return models.BlacklistInfo{
		Dirs:      dirs,
		Paths:     paths,
		Signs:     signs,
		White:     white,
		LocalTime: localTime,
		LocalDir:  rules.LocalDir(),
	}
}

// UpdateBlacklist 从远端下载最新黑名单并热加载（gitee softcnkiller/data）
func (s *RogueService) UpdateBlacklist() models.BlacklistUpdateResult {
	return rules.UpdateBlacklist()
}

// ============ 备份与清理实现 ============

// backupAndClean 按类型备份并执行清理
func backupAndClean(item *models.RogueItem) (*BackupEntry, error) {
	entry := &BackupEntry{
		ID:        item.ID,
		Name:      item.Name,
		RuleType:  item.RuleType,
		RiskLevel: item.RiskLevel,
		Time:      time.Now().Format(time.RFC3339),
	}

	switch item.RuleType {
	case "registry":
		if item.Action == "repair" {
			return backupAndRepairRegistry(item, entry)
		}
		return backupAndRemoveRegistry(item, entry)
	case "startup", "file", "dir":
		return backupAndMoveFile(item, entry)
	case "service":
		return backupAndDisableService(item, entry)
	case "task":
		return backupAndDisableTask(item, entry)
	case "extension", "browser":
		return backupAndRemoveRegistry(item, entry)
	case "namespace", "shellex":
		// 子键（默认值=CLSID/显示名）形态：删除整个子键才能移除该入口
		return backupAndRemoveKey(item, entry)
	case "hosts":
		return backupAndCleanHosts(item, entry)
	case "shortcut":
		return backupAndRepairShortcut(item, entry)
	case "installed":
		return nil, fmt.Errorf("已安装软件请在系统设置中卸载：%s", item.Value)
	}
	return nil, fmt.Errorf("未知类型: %s", item.RuleType)
}

// backupAndRemoveRegistry 备份并删除注册表值（备份保留原始类型与字节，可按原样恢复）
func backupAndRemoveRegistry(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	parts := strings.SplitN(item.Path, "\\", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("无效注册表路径: %s", item.Path)
	}
	hiveName := strings.ToUpper(parts[0])
	hive, err := hiveFromName(hiveName)
	if err != nil {
		return nil, err
	}
	rest := parts[1]
	valName := rest[strings.LastIndex(rest, "\\")+1:]
	keyPath := strings.TrimSuffix(rest, "\\"+valName)

	k, err := registry.OpenKey(hive, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	// GetValue 为低层 API：先传 nil 探测所需字节数与类型，再读数据
	n, valtype, err := k.GetValue(valName, nil)
	if err == nil && n > 0 {
		data := make([]byte, n)
		_, _, err = k.GetValue(valName, data)
		if err == nil {
			entry.RegHive = hiveName
			entry.RegKey = keyPath
			entry.RegValue = valName
			entry.RegKind = valtype
			switch valtype {
			case registry.SZ, registry.EXPAND_SZ:
				entry.RegData = decodeUTF16Bytes(data)
			case registry.DWORD:
				if len(data) >= 4 {
					entry.RegData = fmt.Sprint(binary.LittleEndian.Uint32(data))
				}
			}
			entry.RegBlob = data
		}
	}
	k.Close()
	if err != nil {
		return nil, err
	}

	wk, err := registry.OpenKey(hive, keyPath, registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	defer wk.Close()
	if err := wk.DeleteValue(valName); err != nil {
		return nil, err
	}
	return entry, nil
}

// backupAndRemoveKey 备份整棵子键子树后递归删除
// 用于 Shell 扩展/驱动器菜单项等以「子键」形态挂载的注册表项。
// 注意：这类键常带子键（驱动器右键静态动词的命令就在 Command 子键里），
// 而 RegDeleteKey 拒绝删除非空键（Access is denied）——这正是「驱动器里的能清理、
// 右键菜单项删不掉」的原因。故此处改为：整棵子树（所有值+子键，保留类型与字节）备份
// → 自底向上递归删除 → 复核；恢复时按备份原样重建。
func backupAndRemoveKey(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	parts := strings.SplitN(item.Path, "\\", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("无效注册表路径: %s", item.Path)
	}
	hiveName := strings.ToUpper(parts[0])
	hive, err := hiveFromName(hiveName)
	if err != nil {
		return nil, err
	}
	keyPath := parts[1]

	entry.RegHive = hiveName
	entry.RegKey = keyPath

	// 1) 整棵子树备份（失败不阻塞清理：至少保证默认值被记下）
	if tree, terr := dumpKeyTree(hive, keyPath); terr == nil {
		entry.RegTree = tree
	}
	if k, oerr := registry.OpenKey(hive, keyPath, registry.QUERY_VALUE); oerr == nil {
		def, _, _ := k.GetStringValue("")
		k.Close()
		entry.RegValue = "" // 默认值
		entry.RegKind = registry.SZ
		entry.RegData = def
	}

	// 2) 递归删除（先清子键，再删自身）
	if derr := deleteKeyTree(hive, keyPath); derr != nil {
		return nil, wrapRegDeleteErr(hiveName, derr)
	}

	// 3) 复核：仍存在说明被安全软件的自我防护挡下了
	if _, oerr := registry.OpenKey(hive, keyPath, registry.QUERY_VALUE); oerr == nil {
		return nil, fmt.Errorf("删除后该注册表项仍然存在：多为安全软件自我防护拦截，请在该软件设置中关闭对应功能（或卸载源头软件）后重试")
	}
	return entry, nil
}

// decodeUTF16Bytes 将注册表 REG_SZ/EXPAND_SZ 的 UTF-16LE 原始字节解码为字符串
func decodeUTF16Bytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2)
	for len(u) > 0 && u[len(u)-1] == 0 {
		u = u[:len(u)-1]
	}
	return syscall.UTF16ToString(u)
}

// copyFile 复制文件（用于 hosts/.lnk 等“副本备份 + 原位置重写”的场景）
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// backupAndRepairRegistry 备份注册表值原始内容（按类型/字节，供恢复），
// 然后写入修复值（item.RepairValue）。用于 Userinit 篡改这类
// “不能直接删除、必须恢复为安全值”的场景。
func backupAndRepairRegistry(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	parts := strings.SplitN(item.Path, "\\", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("无效注册表路径: %s", item.Path)
	}
	hiveName := strings.ToUpper(parts[0])
	hive, err := hiveFromName(hiveName)
	if err != nil {
		return nil, err
	}
	rest := parts[1]
	valName := rest[strings.LastIndex(rest, "\\")+1:]
	keyPath := strings.TrimSuffix(rest, "\\"+valName)

	k, err := registry.OpenKey(hive, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	n, valtype, err := k.GetValue(valName, nil)
	if err == nil && n > 0 {
		data := make([]byte, n)
		if _, _, err2 := k.GetValue(valName, data); err2 == nil {
			entry.RegHive = hiveName
			entry.RegKey = keyPath
			entry.RegValue = valName
			entry.RegKind = valtype
			switch valtype {
			case registry.SZ, registry.EXPAND_SZ:
				entry.RegData = decodeUTF16Bytes(data)
			case registry.DWORD:
				if len(data) >= 4 {
					entry.RegData = fmt.Sprint(binary.LittleEndian.Uint32(data))
				}
			}
			entry.RegBlob = data
		}
	}
	k.Close()
	if err != nil {
		return nil, err
	}
	// 写入修复值（保持 REG_SZ 形态；恢复时按备份类型原样还原）
	wk, err := registry.OpenKey(hive, keyPath, registry.SET_VALUE)
	if err != nil {
		return nil, err
	}
	defer wk.Close()
	if err := wk.SetStringValue(valName, item.RepairValue); err != nil {
		return nil, fmt.Errorf("写入修复值失败（需管理员权限）: %w", err)
	}
	return entry, nil
}

// restoreRegistryValue 按备份的类型原样恢复注册表值
func restoreRegistryValue(k registry.Key, name string, e *BackupEntry) error {
	switch e.RegKind {
	case registry.EXPAND_SZ:
		return k.SetExpandStringValue(name, e.RegData)
	case registry.DWORD:
		v := uint32(0)
		if len(e.RegBlob) >= 4 {
			v = binary.LittleEndian.Uint32(e.RegBlob)
		}
		return k.SetDWordValue(name, v)
	case registry.QWORD:
		v := uint64(0)
		if len(e.RegBlob) >= 8 {
			v = binary.LittleEndian.Uint64(e.RegBlob)
		}
		return k.SetQWordValue(name, v)
	case registry.BINARY:
		return k.SetBinaryValue(name, e.RegBlob)
	case registry.MULTI_SZ:
		return setRegistryMultiString(k, name, strings.Split(e.RegData, "; "))
	default:
		// registry.SZ / registry.NONE / 旧版备份（RegKind=0）
		return k.SetStringValue(name, e.RegData)
	}
}

// procRegSetValueExW advapi32.RegSetValueExW（registry 包未暴露，多字符串恢复用）
var procRegSetValueExW = syscall.NewLazyDLL("advapi32.dll").NewProc("RegSetValueExW")

// setRegistryMultiString 按 REG_MULTI_SZ 原样写回（每条以 NUL 结尾，末尾追加双 NUL）
func setRegistryMultiString(k registry.Key, name string, parts []string) error {
	var b []byte
	for _, p := range parts {
		for _, c := range syscall.StringToUTF16(p) {
			b = append(b, byte(c), byte(c>>8))
		}
	}
	b = append(b, 0, 0)
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var dataPtr uintptr
	if len(b) > 0 {
		dataPtr = uintptr(unsafe.Pointer(&b[0]))
	}
	r0, _, e := procRegSetValueExW.Call(
		uintptr(k), uintptr(unsafe.Pointer(namePtr)), 0,
		uintptr(registry.MULTI_SZ), dataPtr, uintptr(len(b)))
	if r0 != 0 {
		return e
	}
	return nil
}

// backupAndMoveFile 备份并移动文件/目录
func backupAndMoveFile(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	orig := item.Path
	info, err := os.Lstat(orig)
	if err != nil {
		return nil, fmt.Errorf("目标不存在或无法访问（可能已被手动删除）: %s", orig)
	}
	idDir := filepath.Join(backupRoot(), item.ID)
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		return nil, err
	}
	backed := filepath.Join(idDir, info.Name())
	// 清理同名旧备份残留（同 ID 二次清理/上次恢复不完整时），避免 Rename 目标已存在
	_ = os.RemoveAll(backed)
	if err := os.Rename(orig, backed); err != nil {
		if info.IsDir() {
			return nil, fmt.Errorf("目录仍被占用无法隔离（%v）。已尝试结束其中进程，若仍失败请关闭相关程序或重启后重试: %s", err, orig)
		}
		return nil, fmt.Errorf("文件被占用或无法移动: %v (%s)", err, orig)
	}
	entry.OrigPath = orig
	entry.BackedPath = backed
	return entry, nil
}

// backupAndDisableService 备份服务启动类型并禁用服务
func backupAndDisableService(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	svcName := item.Path
	keyPath := `SYSTEM\CurrentControlSet\Services\` + svcName

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("读取服务配置失败: %w", err)
	}
	start, _, err := k.GetIntegerValue("Start")
	k.Close()
	if err != nil {
		return nil, fmt.Errorf("读取服务启动类型失败: %w", err)
	}
	entry.ServiceName = svcName
	entry.ServiceStart = uint32(start)

	// 先尝试停止服务（失败不影响禁用）
	_ = runHidden("sc", "stop", svcName)

	// 设置 Start=4（禁用）
	wk, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.SET_VALUE)
	if err != nil {
		return nil, fmt.Errorf("需要管理员权限修改服务: %w", err)
	}
	defer wk.Close()
	if err := wk.SetDWordValue("Start", 4); err != nil {
		return nil, fmt.Errorf("禁用服务失败: %w", err)
	}
	return entry, nil
}

// backupAndDisableTask 备份计划任务 XML 并禁用
func backupAndDisableTask(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	taskName := item.Path
	out, _ := runHiddenOutput("schtasks", "/query", "/tn", taskName, "/xml")
	if len(out) == 0 {
		return nil, fmt.Errorf("读取计划任务失败: %s", taskName)
	}
	entry.TaskName = taskName
	entry.TaskXML = string(out)

	// 禁用任务（需要管理员权限）
	if err := runHidden("schtasks", "/change", "/tn", taskName, "/disable"); err != nil {
		return nil, fmt.Errorf("禁用计划任务失败: %w", err)
	}
	return entry, nil
}

// restoreEntry 恢复单个项目；成功则从清单移除
// restoreEntry 恢复单个项目；成功则从清单移除，返回 (是否成功, 错误详情)
func restoreEntry(id string, manifest *[]BackupEntry) (bool, error) {
	for i := range *manifest {
		e := &(*manifest)[i]
		if e.ID != id {
			continue
		}
		var err error
		switch e.RuleType {
		case "registry", "extension", "browser":
			hive, herr := hiveFromName(e.RegHive)
			if herr != nil {
				err = herr
				break
			}
			k, oerr := registry.OpenKey(hive, e.RegKey, registry.SET_VALUE)
			if oerr != nil {
				err = oerr
				break
			}
			err = restoreRegistryValue(k, e.RegValue, e)
			k.Close()
		case "hosts", "shortcut":
			// 清理时是“整文件备份副本 + 原位置重写”，恢复 = 用副本覆盖回原路径
			if e.BackedPath != "" {
				err = copyFile(e.BackedPath, e.OrigPath)
			}
		case "shellex", "namespace":
			// 清理时删除的是整个子键：恢复 = 重建键并写回原内容
			hive, herr := hiveFromName(e.RegHive)
			if herr != nil {
				err = herr
				break
			}
			// 新版备份含整棵子树（如驱动器右键动词的 Command 子键）→ 原样重建
			if e.RegTree != "" {
				err = restoreKeyTree(hive, e.RegKey, e.RegTree)
				break
			}
			// 旧版备份仅记默认值：重建键并写回（向后兼容既有 manifest）
			k, _, cerr := registry.CreateKey(hive, e.RegKey, registry.SET_VALUE)
			if cerr != nil {
				err = cerr
				break
			}
			err = k.SetStringValue("", e.RegData)
			k.Close()
		case "startup", "file", "dir":
			if e.BackedPath != "" {
				if mErr := os.MkdirAll(filepath.Dir(e.OrigPath), 0o755); mErr == nil {
					err = os.Rename(e.BackedPath, e.OrigPath)
				} else {
					err = mErr
				}
			}
		case "service":
			keyPath := `SYSTEM\CurrentControlSet\Services\` + e.ServiceName
			k, oerr := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.SET_VALUE)
			if oerr != nil {
				err = oerr
				break
			}
			err = k.SetDWordValue("Start", e.ServiceStart)
			k.Close()
			if err == nil {
				_ = runHidden("sc", "start", e.ServiceName)
			}
		case "task":
			if e.TaskName != "" {
				err = runHidden("schtasks", "/change", "/tn", e.TaskName, "/enable")
			}
		}
		if err == nil {
			*manifest = append((*manifest)[:i], (*manifest)[i+1:]...)
			// 文件类条目恢复后清理备份副本（尽力而为）
			if e.BackedPath != "" {
				_ = os.RemoveAll(filepath.Dir(e.BackedPath))
			}
			return true, nil
		}
		return false, err
	}
	return false, fmt.Errorf("未找到对应恢复记录（可能已恢复）")
}

// ============ 工具函数 ============

func genID(kind, a, b string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(kind + "|" + a + "|" + b))
	return fmt.Sprintf("%x", h.Sum64())
}

func hiveFromName(name string) (registry.Key, error) {
	switch name {
	case "HKLM", "HKEY_LOCAL_MACHINE":
		return registry.LOCAL_MACHINE, nil
	case "HKCU", "HKEY_CURRENT_USER":
		return registry.CURRENT_USER, nil
	case "HKCR", "HKEY_CLASSES_ROOT":
		return registry.CLASSES_ROOT, nil
	}
	return 0, fmt.Errorf("不支持的注册表根键: %s", name)
}

func backupRoot() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DriveWise", "backup")
}

func manifestPath() string {
	return filepath.Join(backupRoot(), "manifest.json")
}

func loadManifest() ([]BackupEntry, error) {
	data, err := os.ReadFile(manifestPath())
	if err != nil {
		return nil, err
	}
	var m []BackupEntry
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func saveManifest(m []BackupEntry) error {
	if err := os.MkdirAll(backupRoot(), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(manifestPath(), data, 0o644)
}
