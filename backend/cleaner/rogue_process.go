package cleaner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
	"drivewise/backend/rules"
)

// processInfo 运行中的进程信息
type processInfo struct {
	pid  uint32
	ppid uint32 // 父进程 PID（用于构造进程树，杀子进程）
	name string
	path string
}

// highRiskKeywords 高置信关键词集合��仅包含规则库中 high 风险的明确流氓组件词
// （进程/Shell 扩展等敏感维度只用此集合匹配，避免 wps/bandizip 等正常软件误报）
var highRiskKeywords = func() map[string]*rules.RogueRule {
	m := map[string]*rules.RogueRule{}
	for i := range rules.RogueRules {
		r := &rules.RogueRules[i]
		if r.RiskLevel != "high" {
			continue
		}
		for _, kw := range r.Keywords {
			if kw != "" {
				m[strings.ToLower(kw)] = r
			}
		}
	}
	return m
}()

// matchHighRisk 高置信匹配（返回命中的规则）
func matchHighRisk(text string) *rules.RogueRule {
	lower := strings.ToLower(text)
	for kw, r := range highRiskKeywords {
		if kw != "" && strings.Contains(lower, kw) {
			return r
		}
	}
	return nil
}

// snapshotProcesses 枚举当前所有进程（名称 + 完整路径）
func snapshotProcesses() []processInfo {
	var out []processInfo
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		p := processInfo{pid: entry.ProcessID, ppid: entry.ParentProcessID, name: name}
		if h, oerr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID); oerr == nil {
			var buf [512]uint16
			var size = uint32(len(buf))
			if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil {
				p.path = windows.UTF16ToString(buf[:size])
			}
			windows.CloseHandle(h)
		}
		out = append(out, p)
	}
	return out
}

// scanProcesses 扫描运行中的流氓软件进程
func scanProcesses() []models.RogueItem {
	var items []models.RogueItem
	procs := snapshotProcesses()
	for _, p := range procs {
		if p.name == "" || p.pid <= 4 {
			continue
		}
		// 跳过系统关键进程
		if isSystemProcess(p.name) {
			continue
		}
		// 高置信规则匹配（仅明确流氓组件词，避免正常软件误报）
		if rule := matchHighRisk(p.name + " " + p.path); rule != nil {
			items = append(items, models.RogueItem{
				// ID 用 name+path 而非 PID：清理时后端会重新扫描，
				// PID 变化/进程重启会导致 ID 失配（“未找到对应项目”）而无法执行清理
				ID:        genID("process", p.name, p.path),
				Name:      rule.Name,
				RuleType:  "process",
				Location:  "运行中进程",
				Path:      p.path,
				Value:     p.name,
				RiskLevel: "high",
				Detail:    "进程正在运行：进程名命中规则",
				Impact:    rule.Impact,
				Action:    "kill", // 可执行动作：结束进程
				Selected:  false,
				Running:   true,
				PID:       int(p.pid),
			})
			continue
		}
		// 路径命中黑名单目录
		if p.path != "" && (rules.IsBlacklistedDir(filepath.Base(filepath.Dir(p.path))) || rules.IsBlacklistedPath(p.path)) {
			items = append(items, models.RogueItem{
				ID:        genID("process", p.name, p.path),
				Name:      p.name,
				RuleType:  "process",
				Location:  "运行中进程（黑名单目录）",
				Path:      p.path,
				Value:     p.name,
				RiskLevel: "medium",
				Detail:    "进程运行于黑名单目录",
				Impact:    "已知流氓软件目录中的进程，可能常驻后台/弹窗/自动下载",
				Action:    "kill",
				Selected:  false,
				Running:   true,
				PID:       int(p.pid),
			})
		}
	}
	return items
}

// isSystemProcess 系统关键进程不参与扫描
func isSystemProcess(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "system", "system idle process", "smss.exe", "csrss.exe", "wininit.exe",
		"winlogon.exe", "services.exe", "lsass.exe", "lsm.exe", "svchost.exe",
		"explorer.exe", "dwm.exe", "taskhost.exe", "taskhostw.exe", "sihost.exe",
		"conhost.exe", "runtimebroker.exe", "registry", "memory compression",
		"searchindexer.exe", "fontdrvhost.exe", "ctfmon.exe", "shellexperiencehost.exe",
		"startmenuexperiencehost.exe", "backgroundtaskhost.exe":
		return true
	}
	return false
}

// scanShellEx 扫描 Shell 扩展注入点（explorer 加载的右键菜单/图标处理器 DLL）与 AppInit_DLLs
// 鲁大师等流氓软件通过 ContextMenuHandlers 注入 explorer.exe 实现常驻与右键广告
func scanShellEx() []models.RogueItem {
	var items []models.RogueItem

	// 常见 Shell 扩展注入点
	handlers := []regTarget{
		{registry.CLASSES_ROOT, `*\shellex\ContextMenuHandlers`, "文件右键菜单扩展"},
		{registry.CLASSES_ROOT, `Directory\shellex\ContextMenuHandlers`, "目录右键菜单扩展"},
		{registry.CLASSES_ROOT, `Directory\Background\shellex\ContextMenuHandlers`, "桌面右键菜单扩展"},
		{registry.CLASSES_ROOT, `Folder\shellex\ContextMenuHandlers`, "文件夹右键菜单扩展"},
		{registry.CLASSES_ROOT, `*\shellex\PropertySheetHandlers`, "文件属性页扩展"},
		{registry.CLASSES_ROOT, `Directory\shellex\DragDropHandlers`, "拖拽扩展"},
	}

	for _, t := range handlers {
		k, err := registry.OpenKey(t.hive, t.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		subs, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, sub := range subs {
			// 子键默认值 = CLSID
			sk, err := registry.OpenKey(t.hive, t.path+`\`+sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			clsid, _, _ := sk.GetStringValue("")
			sk.Close()
			if clsid == "" {
				continue
			}
			// 由 CLSID 解析 DLL 路径
			dllPath := resolveCLSIDServer(clsid)
			if dllPath == "" {
				continue
			}
			// 系统扩展（Microsoft 前缀/系统目录）跳过
			if strings.HasPrefix(strings.ToLower(dllPath), `c:\windows\system32`) ||
				strings.HasPrefix(strings.ToLower(dllPath), `c:\windows\syswow64`) {
				continue
			}
			if rule := matchHighRisk(sub + " " + dllPath); rule != nil {
				items = append(items, models.RogueItem{
					ID:        genID("shellex", t.path+`\`+sub, dllPath),
					Name:      rule.Name,
					RuleType:  "shellex",
					Location:  t.desc,
					Path:      `HKCR\` + t.path + `\` + sub,
					Value:     dllPath,
					RiskLevel: "high",
					Detail:    "Shell 扩展 DLL: " + dllPath,
					Impact:    "注入资源管理器（explorer.exe）右键菜单/图标，常驻后台并可能自启",
					Action:    "hint",
					Selected:  false,
				})
			}
		}
	}

	// AppInit_DLLs（全局 DLL 注入点，危险）
	appInit := []regTarget{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Windows`, "AppInit_DLLs 注入"},
	}
	for _, t := range appInit {
		k, err := registry.OpenKey(t.hive, t.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		val, _, _ := k.GetStringValue("AppInit_DLLs")
		enabled, _, _ := k.GetIntegerValue("LoadAppInit_DLLs")
		k.Close()
		if val != "" && enabled != 0 {
			if rule := matchHighRisk(val); rule != nil {
				items = append(items, models.RogueItem{
					ID:        genID("shellex", "AppInit_DLLs", val),
					Name:      rule.Name,
					RuleType:  "registry",
					Location:  "全局 DLL 注入（AppInit_DLLs）",
					Path:      "HKLM\\" + t.path + `\AppInit_DLLs`,
					Value:     val,
					RiskLevel: "high",
					Detail:    "AppInit_DLLs 全局注入: " + val,
					Impact:    "注入所有加载 user32.dll 的进程，危害极大",
					Action:    "remove",
					Selected:  false,
				})
			}
		}
	}
	return items
}

// resolveCLSIDServer 由 CLSID 查询注册的 DLL 路径（InprocServer32）
func resolveCLSIDServer(clsid string) string {
	clsid = strings.Trim(clsid, "{}")
	keyPath := `CLSID\{` + clsid + `}\InprocServer32`
	k, err := registry.OpenKey(registry.CLASSES_ROOT, keyPath, registry.QUERY_VALUE)
	if err != nil {
		// WOW6432Node
		keyPath = `WOW6432Node\CLSID\{` + clsid + `}\InprocServer32`
		k, err = registry.OpenKey(registry.CLASSES_ROOT, keyPath, registry.QUERY_VALUE)
		if err != nil {
			return ""
		}
	}
	defer k.Close()
	val, _, err := k.GetStringValue("")
	if err != nil || val == "" {
		return ""
	}
	return val
}

// terminateProcessByPID 直接通过系统 API 结束进程。
// 不使用 taskkill.exe：安全软件常把 taskkill/rundll32 等工具当作 LOLBin 静默拦截，
// 导致外部 exe 调用看似成功、进程却未被结束（本项目实测过的坑）。
func terminateProcessByPID(pid uint32) error {
	if pid == 0 || pid == 4 { // System / System Idle 保护
		return fmt.Errorf("拒绝结束系统进程 PID=%d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// appendUnique 去重追加
func appendUnique(list []string, v string) []string {
	for _, e := range list {
		if e == v {
			return list
		}
	}
	return append(list, v)
}

// collectSubtree 收集 roots 及其全部子孙进程 PID（按 ppid 关系，广度优先）。
// 用于把守护/子进程一锅端，避免只杀主进程后子进程继续存活或守护进程被拉起。
func collectSubtree(procs []processInfo, roots map[uint32]bool) []uint32 {
	children := map[uint32][]uint32{}
	for _, p := range procs {
		if p.ppid != 0 && p.ppid != p.pid {
			children[p.ppid] = append(children[p.ppid], p.pid)
		}
	}
	var order []uint32
	seen := map[uint32]bool{}
	var walk func(pid uint32)
	walk = func(pid uint32) {
		if seen[pid] {
			return
		}
		seen[pid] = true
		order = append(order, pid)
		for _, c := range children[pid] {
			walk(c)
		}
	}
	// 按 PID 升序遍历 roots，保证顺序稳定
	pids := make([]uint32, 0, len(roots))
	for pid := range roots {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	for _, pid := range pids {
		walk(pid)
	}
	return order
}

// killProcessesByKeyword 终止所有名称或路径命中关键词的进程及其进程树
// 返回实际被结束的进程名列表（去重）
func killProcessesByKeyword(keyword string) []string {
	var killed []string
	procs := snapshotProcesses()
	if len(procs) == 0 {
		return nil
	}
	lower := strings.ToLower(keyword)
	roots := map[uint32]bool{}
	byPID := map[uint32]string{}
	for _, p := range procs {
		byPID[p.pid] = p.name
		if isSystemProcess(p.name) {
			continue
		}
		haystack := strings.ToLower(p.name + " " + p.path)
		if strings.Contains(haystack, lower) {
			roots[p.pid] = true
		}
	}
	if len(roots) == 0 {
		return nil
	}
	order := collectSubtree(procs, roots)
	// 先杀子孙后杀祖先：父先死会导致子进程被孤儿化并可能被守护逻辑接管
	for i := len(order) - 1; i >= 0; i-- {
		if err := terminateProcessByPID(order[i]); err == nil {
			if name, ok := byPID[order[i]]; ok {
				killed = appendUnique(killed, name)
			}
		}
	}
	return killed
}

// killProcessesUnderDir 终止运行于指定目录（含子目录）下的所有进程及其进程树。
// 用途：清理目录类流氓软件时，目录内的 exe 常驻运行会占用文件句柄，导致整体
// 迁移（os.Rename 隔离）失败；先按“进程完整路径前缀”定位并结束它们再清理。
func killProcessesUnderDir(dir string) []string {
	if dir == "" {
		return nil
	}
	prefix := strings.ToLower(filepath.Clean(dir)) + `\`
	var killed []string
	procs := snapshotProcesses()
	if len(procs) == 0 {
		return nil
	}
	roots := map[uint32]bool{}
	byPID := map[uint32]string{}
	for _, p := range procs {
		if p.path == "" || p.pid <= 4 || isSystemProcess(p.name) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(filepath.Clean(p.path)), prefix) {
			roots[p.pid] = true
			byPID[p.pid] = p.name
		}
	}
	if len(roots) == 0 {
		return nil
	}
	order := collectSubtree(procs, roots)
	// 先杀子孙后杀祖先
	for i := len(order) - 1; i >= 0; i-- {
		if err := terminateProcessByPID(order[i]); err == nil {
			if name, ok := byPID[order[i]]; ok {
				killed = appendUnique(killed, name)
			}
		}
	}
	return killed
}

// processRunningByName 检查是否存在匹配关键词的进程
func processRunningByName(keyword string) bool {
	procs := snapshotProcesses()
	lower := strings.ToLower(keyword)
	for _, p := range procs {
		if strings.Contains(strings.ToLower(p.name+" "+p.path), lower) && !isSystemProcess(p.name) {
			return true
		}
	}
	return false
}

// markRebootDelete 文件被占用时标记系统重启后删除
func markRebootDelete(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(pathPtr, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

// checkFileExists 文件是否存在（辅助）
func checkFileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
