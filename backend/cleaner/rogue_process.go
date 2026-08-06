package cleaner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
	"drivewise/backend/rules"
)

// processInfo 运行中的进程信息
type processInfo struct {
	pid  uint32
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
		p := processInfo{pid: entry.ProcessID, name: name}
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
				ID:        genID("process", p.name, fmt.Sprint(p.pid)),
				Name:      rule.Name,
				RuleType:  "process",
				Location:  "运行中进程",
				Path:      p.path,
				Value:     p.name,
				RiskLevel: "high",
				Detail:    "进程正在运行：进程名命中规则",
				Impact:    rule.Impact,
				Action:    "hint",
				Selected:  false,
				Running:   true,
				PID:       int(p.pid),
			})
			continue
		}
		// 路径命中黑名单目录
		if p.path != "" && (rules.IsBlacklistedDir(filepath.Base(filepath.Dir(p.path))) || rules.IsBlacklistedPath(p.path)) {
			items = append(items, models.RogueItem{
				ID:        genID("process", p.name, fmt.Sprint(p.pid)),
				Name:      p.name,
				RuleType:  "process",
				Location:  "运行中进程（黑名单目录）",
				Path:      p.path,
				Value:     p.name,
				RiskLevel: "medium",
				Detail:    "进程运行于黑名单目录",
				Impact:    "已知流氓软件目录中的进程，可能常驻后台/弹窗/自动下载",
				Action:    "hint",
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
					Path:      t.path + `\` + sub,
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
					RuleType:  "shellex",
					Location:  "全局 DLL 注入（AppInit_DLLs）",
					Path:      t.path,
					Value:     val,
					RiskLevel: "high",
					Detail:    "AppInit_DLLs 全局注入: " + val,
					Impact:    "注入所有加载 user32.dll 的进程，危害极大",
					Action:    "hint",
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

// killProcessesByKeyword 终止所有名称或路径命中关键词的进程（taskkill /F /T 强杀进程树）
func killProcessesByKeyword(keyword string) []string {
	var killed []string
	procs := snapshotProcesses()
	lower := strings.ToLower(keyword)
	for _, p := range procs {
		haystack := strings.ToLower(p.name + " " + p.path)
		if !strings.Contains(haystack, lower) {
			continue
		}
		if isSystemProcess(p.name) {
			continue
		}
		if err := taskkill(p.pid, p.name); err == nil {
			killed = append(killed, p.name)
		}
	}
	return killed
}

// taskkill 强制终止进程及其子进程树
func taskkill(pid uint32, name string) error {
	cmd := exec.Command("taskkill.exe", "/PID", fmt.Sprint(pid), "/F", "/T")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
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
