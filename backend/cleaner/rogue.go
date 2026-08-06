package cleaner

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	RegHive      string `json:"regHive,omitempty"` // HKLM | HKCU
	RegKey       string `json:"regKey,omitempty"`  // 注册表键路径
	RegValue     string `json:"regValue,omitempty"` // 值名
	RegData      string `json:"regData,omitempty"` // 原值数据
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
//      运行中进程/Shell 扩展注入点
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
	return items
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
		sk.Close()
		if rule := matchService(sub, display+" "+img); rule != nil {
			action := rule.Action
			if action == "remove" {
				action = "disable_service" // 服务统一采用禁用方式，避免误删系统组件
			}
			items = append(items, rogueItem("service", rule, "Windows 服务", sub, display, action))
		}
	}
	k.Close()
	return items
}

// scanTasks 扫描计划任务（schtasks 列表，匹配任务名）
func scanTasks() []models.RogueItem {
	var items []models.RogueItem
	cmd := exec.Command("schtasks", "/query", "/fo", "CSV", "/nh")
	cmd.SysProcAttr = hideWindow()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
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

// scanBlackDirs 扫描常见安装目录，匹配 softcnkiller 目录黑名单
func scanBlackDirs() []models.RogueItem {
	var items []models.RogueItem
	roots := []string{
		`C:\Program Files`,
		`C:\Program Files (x86)`,
		filepath.Join(os.Getenv("LOCALAPPDATA")),
		filepath.Join(os.Getenv("APPDATA")),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs"),
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// 排除系统保留目录（避免把 Temp/Microsoft 等误报为流氓软件）
			if systemDirName(e.Name()) {
				continue
			}
			full := filepath.Join(root, e.Name())
			if rules.IsWhitelistedPath(full) {
				continue
			}
			// 目录名黑名单 / 相对路径黑名单
			if rules.IsBlacklistedDir(e.Name()) || rules.IsBlacklistedPath(full) {
				items = append(items, models.RogueItem{
					ID:        genID("dir", full, ""),
					Name:      e.Name(),
					RuleType:  "dir",
					Location:  "安装目录（softcnkiller 黑名单）",
					Path:      full,
					Value:     e.Name(),
					RiskLevel: "medium",
					Detail:    "目录名命中流氓软件黑名单",
					Impact:    "已知流氓软件安装目录，可能存在捆绑/弹窗/广告行为",
					Action:    "hint",
					Selected:  false,
				})
			}
		}
	}
	return items
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

// CleanRogue 清理选中项并记录备份
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
			results = append(results, models.RogueCleanResult{ID: id, OK: false, ErrMsg: "未找到对应项目"})
			continue
		}
		if item.Action == "hint" {
			results = append(results, models.RogueCleanResult{ID: id, OK: false, ErrMsg: "该项目仅提示，请在系统设置中自行处理: " + item.Value})
			continue
		}
		// 对抗进程注入：清理前强杀相关进程（名称/路径命中关键词）
		if killed := killProcessesForItem(item); len(killed) > 0 {
			results = append(results, models.RogueCleanResult{ID: id, OK: true, ErrMsg: "已终止进程: " + strings.Join(killed, ", ") + "（可能需重启后完全生效）"})
		}
		entry, err := backupAndClean(item)
		if err != nil {
			results = append(results, models.RogueCleanResult{ID: id, OK: false, ErrMsg: err.Error()})
			continue
		}
		if entry != nil {
			manifest = append(manifest, *entry)
		}
		results = append(results, models.RogueCleanResult{ID: id, OK: true})
	}

	_ = saveManifest(manifest)
	return results
}

// killProcessesForItem 清理前终止相关进程（按条目名称/路径提取关键词）
func killProcessesForItem(item *models.RogueItem) []string {
	var keywords []string
	if item.PID > 0 {
		// 直接按 PID 终止
		if err := taskkill(uint32(item.PID), ""); err == nil {
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
		ok := restoreEntry(id, &manifest)
		results = append(results, models.RogueCleanResult{ID: id, OK: ok})
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
		return backupAndRemoveRegistry(item, entry)
	case "startup", "file":
		return backupAndMoveFile(item, entry)
	case "service":
		return backupAndDisableService(item, entry)
	case "task":
		return backupAndDisableTask(item, entry)
	case "extension":
		return backupAndRemoveRegistry(item, entry)
	case "installed":
		return nil, fmt.Errorf("已安装软件请在系统设置中卸载：%s", item.Value)
	}
	return nil, fmt.Errorf("未知类型: %s", item.RuleType)
}

// backupAndRemoveRegistry 备份并删除注册表值
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
	data, _, err := k.GetStringValue(valName)
	k.Close()
	if err != nil {
		return nil, err
	}
	entry.RegHive = hiveName
	entry.RegKey = keyPath
	entry.RegValue = valName
	entry.RegData = data

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

// backupAndMoveFile 备份并移动文件/目录
func backupAndMoveFile(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	orig := item.Path
	info, err := os.Lstat(orig)
	if err != nil {
		return nil, err
	}
	idDir := filepath.Join(backupRoot(), item.ID)
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		return nil, err
	}
	backed := filepath.Join(idDir, info.Name())
	if err := os.Rename(orig, backed); err != nil {
		return nil, err
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
func restoreEntry(id string, manifest *[]BackupEntry) bool {
	for i := range *manifest {
		e := &(*manifest)[i]
		if e.ID != id {
			continue
		}
		var err error
		switch e.RuleType {
		case "registry", "extension":
			var hive registry.Key
			if e.RegHive == "HKLM" {
				hive = registry.LOCAL_MACHINE
			} else {
				hive = registry.CURRENT_USER
			}
			k, oerr := registry.OpenKey(hive, e.RegKey, registry.SET_VALUE)
			if oerr != nil {
				err = oerr
				break
			}
			err = k.SetStringValue(e.RegValue, e.RegData)
			k.Close()
		case "startup", "file":
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
			return true
		}
		return false
	}
	return false
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
