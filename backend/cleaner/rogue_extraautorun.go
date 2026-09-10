package cleaner

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
	"drivewise/backend/rules"
)

// ============ 更隐蔽的自启/注入点扫描 ============
//
// 在常规 Run 键之外，顽固流氓软件还会选择以下“正常软件几乎不用、
// 但系统会执行”的位置驻留：
//  1. Winlogon\Userinit —— 登录时执行的初始化程序（可追加恶意项，高危）
//  2. ShellServiceObjectDelayLoad —— 资源管理器启动时加载的全局对象（常驻）
//  3. Active Setup Installed Components —— 登录/新用户配置时执行的组件
//
// 前两类“位置本身即敏感”：Userinit 篡改直接修复（repair），其余位置
// 仍需命中规则/黑名单才报，避免把系统自身组件误判。

// defaultUserinit Winlogon Userinit 的合法值（修复目标）
const defaultUserinit = `C:\Windows\system32\userinit.exe,`

// validUserinitPath 唯一合法的 userinit 路径（大小写不敏感比较）
const validUserinitPath = `c:\windows\system32\userinit.exe`

// scanExtraAutorun 扫描隐蔽自启/注入点
func scanExtraAutorun() []models.RogueItem {
	var items []models.RogueItem
	items = append(items, scanUserinit()...)
	items = append(items, scanShellServiceObjects()...)
	items = append(items, scanActiveSetup()...)
	return items
}

// scanUserinit 检查 Winlogon Userinit 是否被追加/替换（高危登录劫持）
func scanUserinit() []models.RogueItem {
	const keyPath = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	val := readRegString(k, "Userinit")
	k.Close()
	if val == "" {
		return nil // 未配置（极少数精简系统），不打扰
	}
	// 按逗号拆分多个启动程序；首个应始终是系统 userinit.exe
	var suspicious []string
	hasValid := false
	for _, part := range strings.Split(val, ",") {
		p := strings.Trim(strings.TrimSpace(part), `"`)
		if p == "" {
			continue
		}
		if strings.EqualFold(p, validUserinitPath) {
			hasValid = true
			continue
		}
		suspicious = append(suspicious, p)
	}
	// 存在非法项，或合法项缺失且整条被替换 → 需要修复
	if len(suspicious) == 0 && hasValid {
		return nil
	}
	detail := "检测到异常登录项: " + strings.Join(suspicious, "; ")
	if len(suspicious) == 0 {
		detail = "Userinit 中缺少系统默认项: " + val
	}
	return []models.RogueItem{{
		ID:          genID("hijack", "Userinit", val),
		Name:        "Winlogon Userinit 登录劫持",
		RuleType:    "registry",
		Location:    "系统登录自启（高危）",
		Path:        "HKLM\\" + keyPath + `\Userinit`,
		Value:       val,
		RepairValue: defaultUserinit,
		RiskLevel:   "high",
		Detail:      detail,
		Impact:      "每次登录都会先执行被追加的恶意程序，是顽固流氓软件常用的自保手段",
		Action:      "repair",
		Selected:    false,
	}}
}

// scanShellServiceObjects 扫描 ShellServiceObjectDelayLoad（explorer 常驻对象）
// 键下每个值 = CLSID → 解析注册的 DLL，非系统目录且命中规则/黑名单才报
func scanShellServiceObjects() []models.RogueItem {
	const keyPath = `SOFTWARE\Microsoft\Windows\CurrentVersion\ShellServiceObjectDelayLoad`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	names, _ := k.ReadValueNames(-1)
	var items []models.RogueItem
	for _, name := range names {
		clsid := strings.TrimSpace(readRegString(k, name))
		if clsid == "" {
			continue
		}
		dll := resolveCLSIDServer(clsid)
		if dll == "" || isSystemExePath(dll) || rules.IsWhitelistedPath(dll) {
			continue
		}
		if !suspiciousResolved(dll, clsid) {
			continue
		}
		items = append(items, models.RogueItem{
			ID:        genID("shellex", "ShellServiceObjectDelayLoad", name),
			Name:      "Shell 服务对象（常驻）",
			RuleType:  "registry",
			Location:  "资源管理器启动注入",
			Path:      "HKLM\\" + keyPath + `\` + name,
			Value:     dll,
			RiskLevel: "high",
			Detail:    "explorer 启动即加载对象 DLL: " + dll,
			Impact:    "注入资源管理器实现常驻，流氓软件常用于弹窗与自启动保护",
			Action:    "remove",
			Selected:  false,
		})
	}
	k.Close()
	return items
}

// scanActiveSetup 扫描 Active Setup Installed Components 的 StubPath
// 该位置被流氓软件用作“登录时静默执行”，键名多为随机 GUID 无法关键词匹配，
// 因此只报“可执行文件指向非系统目录且命中规则/黑名单”的项
func scanActiveSetup() []models.RogueItem {
	targets := []regTarget{
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Active Setup\Installed Components`, "Active Setup 组件(HKLM)"},
		{registry.CURRENT_USER, `SOFTWARE\Microsoft\Active Setup\Installed Components`, "Active Setup 组件(HKCU)"},
	}
	var items []models.RogueItem
	for _, t := range targets {
		hivePrefix := "HKLM"
		if t.hive == registry.CURRENT_USER {
			hivePrefix = "HKCU"
		}
		k, err := registry.OpenKey(t.hive, t.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		subs, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, sub := range subs {
			sk, err := registry.OpenKey(t.hive, t.path+`\`+sub, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			stub := readRegString(sk, "StubPath")
			sk.Close()
			if stub == "" {
				continue
			}
			exe := extractExePath(stub)
			if exe == "" || !suspiciousResolved(exe, stub) {
				continue // 系统/白名单/文件不存在，或未命中规则
			}
			items = append(items, models.RogueItem{
				ID:        genID("active", t.path, sub),
				Name:      "Active Setup 静默执行项",
				RuleType:  "registry",
				Location:  t.desc,
				Path:      hivePrefix + `\` + t.path + `\` + sub + `\StubPath`,
				Value:     stub,
				RiskLevel: "medium",
				Detail:    "登录/用户配置时静默执行: " + stub,
				Impact:    "流氓软件借该位置登录时自动执行，且隐藏于正常软件列表之外",
				Action:    "remove",
				Selected:  false,
			})
		}
	}
	return items
}

// suspiciousResolved 由解析出的可执行文件/原串判断是否可疑（规则或黑名单命中）
func suspiciousResolved(exeOrDll, raw string) bool {
	if matchHighRisk(raw+" "+exeOrDll) != nil {
		return true
	}
	dir := filepath.Base(filepath.Dir(exeOrDll))
	return rules.IsBlacklistedDir(dir) || rules.IsBlacklistedPath(exeOrDll)
}
