package cleaner

import (
	"strings"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
)

// ============ 系统启动劫持点扫描 ============
//
// 顽固流氓软件/浏览器劫持常用非常规自启位置（正常软件几乎不使用），
// 这些位置“存在非常规值”本身即可疑，无需关键词匹配：
//  1. Winlogon Shell —— 替换系统 shell（高危）
//  2. Explorer\Run（Policies 与 CurrentVersion）—— 静默自启
//  3. IFEO Debugger —— 劫持指定 exe 的启动（高危，浏览器/系统进程被劫持）
// 所有命中项均可一键清理（删除注册表值）并支持恢复。

// scanHijackPoints 扫描系统启动劫持点
func scanHijackPoints() []models.RogueItem {
	var items []models.RogueItem
	items = append(items, scanWinlogonShell()...)
	items = append(items, scanExplorerRun()...)
	items = append(items, scanIFEO()...)
	return items
}

// winlogonShell 检查 Winlogon Shell 是否被替换（合法值：explorer.exe）
func scanWinlogonShell() []models.RogueItem {
	const keyPath = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	shell, _, err := k.GetStringValue("Shell")
	if err != nil {
		return nil
	}
	shell = strings.TrimSpace(strings.Trim(shell, `"`))
	if shell == "" {
		return nil
	}
	// 合法形式仅 explorer.exe（可带路径/参数形式如 explorer.exe）
	base := strings.ToLower(shell)
	if idx := strings.IndexAny(base, " /"); idx > 0 {
		base = base[:idx]
	}
	if base == "explorer.exe" {
		return nil
	}
	return []models.RogueItem{{
		ID:        genID("hijack", "Winlogon\\Shell", shell),
		Name:      "Winlogon Shell 被替换",
		RuleType:  "registry",
		Location:  "系统登录启动劫持（高危）",
		Path:      "HKLM\\" + keyPath + "\\Shell",
		Value:     shell,
		RiskLevel: "high",
		Detail:    "系统 Shell 被替换为: " + shell + "（正常应为 explorer.exe）",
		Impact:    "开机即被劫持加载，常见于顽固流氓软件/浏览器劫持",
		Action:    "remove",
		Selected:  false,
	}}
}

// explorerRunTargets 非常规 Explorer\Run 启动键（正常软件基本不用）
var explorerRunTargets = []regTarget{
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\Explorer\Run`, "HKLM 策略启动项"},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\Explorer\Run`, "HKCU 策略启动项"},
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\Run`, "HKLM Explorer 启动项"},
	{registry.CURRENT_USER, `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\Run`, "HKCU Explorer 启动项"},
}

// scanExplorerRun 扫描非常规 Explorer\Run 启动键
func scanExplorerRun() []models.RogueItem {
	var items []models.RogueItem
	for _, t := range explorerRunTargets {
		hivePrefix := "HKLM"
		if t.hive == registry.CURRENT_USER {
			hivePrefix = "HKCU"
		}
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
			items = append(items, models.RogueItem{
				ID:        genID("hijack", "ExplorerRun", hivePrefix+`\`+t.path+`\`+name),
				Name:      "Explorer\\Run 启动项",
				RuleType:  "registry",
				Location:  t.desc + "（非常规启动点）",
				Path:      hivePrefix + `\` + t.path + `\` + name,
				Value:     data,
				RiskLevel: "medium",
				Detail:    "非常规启动位置出现启动项: " + data,
				Impact:    "正常软件极少使用该位置，多为流氓/推广软件静默自启",
				Action:    "remove",
				Selected:  false,
			})
		}
		k.Close()
	}
	return items
}

// scanIFEO 扫描 IFEO Debugger 劫持（Debugger 值非空即异常）
func scanIFEO() []models.RogueItem {
	const base = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Image File Execution Options`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, base, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()

	var items []models.RogueItem
	for _, sub := range subs {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, base+`\`+sub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		debugger, _, derr := sk.GetStringValue("Debugger")
		sk.Close()
		if derr != nil {
			continue
		}
		debugger = strings.TrimSpace(debugger)
		if debugger == "" {
			continue
		}
		// 系统自身调试用途的项跳过（VerifierDlls/GlobalFlag 等无 Debugger，不会到这里）
		items = append(items, models.RogueItem{
			ID:        genID("hijack", "IFEO", sub),
			Name:      "IFEO Debugger 劫持",
			RuleType:  "registry",
			Location:  "映像劫持（IFEO，高危）",
			Path:      "HKLM\\" + base + `\` + sub + `\Debugger`,
			Value:     debugger,
			RiskLevel: "high",
			Detail:    "程序 " + sub + " 的启动被劫持到: " + debugger,
			Impact:    "目标程序每次启动都会被替换为劫持程序（常用于劫持浏览器/系统工具）",
			Action:    "remove",
			Selected:  false,
		})
	}
	return items
}
