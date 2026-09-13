package cleaner

import (
	"strings"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
)

// ============ 外壳命名空间 / 驱动器菜单残留扫描 ============
//
// 360、腾讯电脑管家、网盘等软件会在「此电脑」的「设备和驱动器」区域、以及驱动器
// 右键菜单里插入自家入口（如「C盘瘦身」「一键加速」）。这些入口不是文件，而是注册表
// 登记的外壳命名空间扩展，因此「右键删图标」永远删不掉，必须删注册表子键：
//
//	MyComputer\NameSpace\{CLSID}            ← 子键名就是 CLSID，默认值 = 显示名
//	Desktop\NameSpace\{CLSID}              ← 同上（桌面图标区）
//	Drive\shellex\ContextMenuHandlers\{名}  ← 默认值 = CLSID
//	Drive\shell\{名}                       ← 静态菜单项，默认值 = 菜单文字
//
// 清理 = 备份子键默认值后删除整个子键（RuleType=namespace → backupAndRemoveKey）；
// 恢复 = 重建子键并写回默认值（restoreEntry 的 namespace/shellex 分支）。
//
// 注意：只删注册表入口还不够——源头软件（360/管家）下次启动会重建，
// 因此 Detail/Impact 里明确提示用户去源头软件关闭对应开关。

// systemNameSpaceGUIDs Windows 自带的「此电脑 / 桌面」命名空间 GUID，严禁清理。
// （下载/文档/图片/音乐/视频/桌面 —— 即 These 11 个 CLSID_ThisPC*RegFolder）
var systemNameSpaceGUIDs = map[string]bool{
	"{088E3905-0323-4B02-9826-5D99428E115F}": true, // 下载
	"{1CF1260C-4DD0-4EBB-811F-33C572699FDE}": true, // 音乐
	"{24AD3AD4-A569-4530-98E1-AB02F9417AA8}": true, // 图片
	"{374DE290-123F-4565-9164-39C4925E467B}": true, // 下载
	"{3ADD1653-EB32-4CB0-BBD7-DFA0ABB5ACCA}": true, // 图片
	"{3DFDF296-DBEC-4FB4-81D1-6A3438BCF4DE}": true, // 音乐
	"{A0953C92-50DC-43BF-BE83-3742FED03C9C}": true, // 视频
	"{A8CDFF1C-4878-43BE-B5FD-F8091C1C60D0}": true, // 文档
	"{B4BFCC3A-DB2C-424C-B029-7FE99A87C641}": true, // 桌面
	"{D3162B92-9365-467A-956B-92703ACA08AF}": true, // 文档
	"{F86FA3AB-70D2-4FC7-9C99-FCBF05467F3A}": true, // 视频
}

// windowsDriveVerbs Windows 自带的驱动器右键静态菜单项（按名称小写比对），严禁清理。
// 名称白名单只是第一道防线，后面还有「实现位于 C:\Windows 则跳过」兜底。
var windowsDriveVerbs = map[string]bool{
	"change-passphrase": true, "change-pin": true, "changepin": true,
	"cmd": true, "encrypt-bde": true, "encrypt-bde-elev": true,
	"find": true, "manage-bde": true, "pintohome": true, "powershell": true,
	"resume-bde": true, "resume-bde-elev": true, "unlock-bde": true,
	"bitlocker": true, "open": true, "properties": true,
}

// nsTarget 命名空间扫描目标
type nsTarget struct {
	hive     registry.Key
	hiveName string // HKLM | HKCU（备份/清理时还原 hive 用）
	path     string
	desc     string
	// loose=true：非系统自带的第三方项一律列出（用户可见区域里的推广入口/残留）
	// loose=false：仅列出关键词/签名黑名单命中项（避免把系统正常项误报成残留）
	loose bool
	// clsidIsKeyName=true：子键名是 CLSID、默认值是显示名；false：默认值是 CLSID
	clsidIsKeyName bool
	// staticVerb=true：静态菜单项（Drive\shell），子键名不是 GUID，命令在 Command 子键
	staticVerb bool
}

var nsTargets = []nsTarget{
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace`, "此电脑 · 设备与驱动器图标", true, true, false},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace`, "此电脑 · 设备与驱动器图标", true, true, false},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace`, "此电脑 · 设备与驱动器图标(32位)", true, true, false},
	// 桌面图标区：系统正常项极多，只报命中规则的
	{registry.CURRENT_USER, "HKCU", `Software\Microsoft\Windows\CurrentVersion\Explorer\Desktop\NameSpace`, "桌面命名空间图标", false, true, false},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Classes\Drive\shellex\ContextMenuHandlers`, "驱动器右键菜单扩展", true, false, false},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Classes\WOW6432Node\Drive\shellex\ContextMenuHandlers`, "驱动器右键菜单扩展(32位)", true, false, false},
	{registry.CURRENT_USER, "HKCU", `Software\Classes\Drive\shellex\ContextMenuHandlers`, "驱动器右键菜单扩展", true, false, false},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Classes\Drive\shell`, "驱动器右键菜单项", true, false, true},
	{registry.LOCAL_MACHINE, "HKLM", `SOFTWARE\Classes\WOW6432Node\Drive\shell`, "驱动器右键菜单项(32位)", true, false, true},
	{registry.CURRENT_USER, "HKCU", `Software\Classes\Drive\shell`, "驱动器右键菜单项", true, false, true},
}

// scanNamespaceExt 扫描「设备与驱动器 / 驱动器右键菜单」里的第三方命名空间扩展与残留
func scanNamespaceExt() []models.RogueItem {
	var items []models.RogueItem
	for _, t := range nsTargets {
		items = append(items, scanNamespaceTarget(t)...)
	}
	return items
}

// namespaceRoots 允许清理的命名空间/驱动器菜单根路径（小写，含 hive 前缀）
var namespaceRoots = func() []string {
	out := make([]string, 0, len(nsTargets))
	for _, t := range nsTargets {
		out = append(out, strings.ToLower(t.hiveName+`\`+t.path+`\`))
	}
	return out
}()

// isKnownNamespacePath 校验清理目标确实是「已知命名空间根键下的某个子键」，
// 防止外部构造的越权调用删除任意注册表键（与 dir/file 类白名单校验同思路）。
func isKnownNamespacePath(p string) bool {
	low := strings.ToLower(strings.TrimSpace(p))
	if low == "" {
		return false
	}
	for _, root := range namespaceRoots {
		if strings.HasPrefix(low, root) && len(low) > len(root) {
			return true
		}
	}
	return false
}

// scanNamespaceTarget 扫描单个目标键下的全部子键
func scanNamespaceTarget(t nsTarget) []models.RogueItem {
	k, err := registry.OpenKey(t.hive, t.path, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if err != nil {
		return nil // 键不存在（多数机器 Drive\shell 之外为空）
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()

	var items []models.RogueItem
	for _, sub := range subs {
		if sub == "DelegateFolders" {
			continue // 系统容器键
		}
		if t.staticVerb && windowsDriveVerbs[strings.ToLower(sub)] {
			continue // Windows 自带的右键动词
		}

		keyPath := t.path + `\` + sub
		sk, err := registry.OpenKey(t.hive, keyPath, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		def, _, _ := sk.GetStringValue("")
		sk.Close()

		var clsid, display, impl string
		switch {
		case t.clsidIsKeyName:
			// 子键名即 CLSID，默认值是显示名
			clsid, display = sub, def
			if systemNameSpaceGUIDs[strings.ToUpper(strings.TrimSpace(sub))] {
				continue // Windows 自带文件夹（下载/文档/图片…）
			}
			impl = resolveCLSIDServer(clsid)
		case t.staticVerb:
			// 静态动词：默认值是菜单文字，实现在 Command 子键
			display = def
			impl = commandExe(readCommandValue(t.hive, keyPath))
		default:
			// ContextMenuHandlers：子键名多为友好名、默认值 = CLSID；
			// 但部分系统项用 GUID 作子键名且默认值为空（如 twext/wpdshext/shell32 的项），
			// 此时回退用子键名当 CLSID，否则会被误判成“卸载残留”。
			display = sub
			clsid = strings.TrimSpace(def)
			if clsid == "" && looksLikeGUID(sub) {
				clsid = sub
			}
			impl = resolveCLSIDServer(clsid)
		}

		// 实现组件位于系统目录 = Windows 内置（shell32/twext/ntshrui…），跳过。
		// 注意：InprocServer32 常用 %SystemRoot% 环境变量，必须先展开再判断。
		if isSystemComponent(impl) {
			continue
		}

		pub := signerOf(impl)
		risk, reason, hit := gradeNamespaceItem(sub, display, impl, pub)
		if !t.loose && !hit {
			continue // 非用户可见区域的第三方项，未命中规则时不展示，避免噪音
		}
		// 既无 CLSID 引用、也无实现组件，且未命中规则 → 无法辨识，不展示
		if !hit && clsid == "" && impl == "" {
			continue
		}

		items = append(items, models.RogueItem{
			ID:        genID("namespace", t.hiveName+`\`+keyPath, sub),
			Name:      namespaceDisplayName(display, sub),
			RuleType:  "namespace",
			Location:  t.desc,
			Path:      t.hiveName + `\` + keyPath,
			Value:     display,
			RiskLevel: risk,
			Detail:    namespaceDetail(reason, impl, pub),
			Impact:    "占用「此电脑 / 设备与驱动器 / 驱动器右键菜单」入口，点击后常推送安装自家全家桶；清理只删该入口，不删除任何文件",
			Action:    "remove",
			Selected:  false, // 参考 RogueCleaner：默认不勾选
		})
	}
	return items
}

// readCommandValue 读取静态动词 Command 子键的默认值（命令行）
func readCommandValue(hive registry.Key, keyPath string) string {
	k, err := registry.OpenKey(hive, keyPath+`\Command`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	cmd, _, _ := k.GetStringValue("")
	return cmd
}

// commandExe 从命令行里解析出 exe 路径（展开 %VAR%，不校验文件是否存在）。
// 与 extractExePath 的区别：不做存在性/白名单过滤，用于识别「指向已卸载程序的残留动词」。
func commandExe(cmd string) string {
	cmd = strings.TrimSpace(winutil.ExpandEnv(cmd))
	if cmd == "" {
		return ""
	}
	if strings.HasPrefix(cmd, `"`) {
		if i := strings.IndexByte(cmd[1:], '"'); i >= 0 {
			cmd = cmd[1 : 1+i]
		}
	} else if i := strings.IndexAny(cmd, " /"); i > 0 {
		cmd = cmd[:i]
	}
	return strings.Trim(strings.TrimSpace(cmd), `"`)
}

// isSystemComponent 实现组件位于 C:\Windows 或 Microsoft 目录 → 视为系统内置。
// 注册表里的 InprocServer32 常写成 %SystemRoot%\system32\xxx.dll，必须先展开环境变量。
func isSystemComponent(p string) bool {
	if p == "" {
		return false
	}
	low := strings.ToLower(winutil.ExpandEnv(p))
	low = strings.ReplaceAll(low, "/", `\`)
	low = strings.TrimPrefix(low, `\\?\`)
	return strings.HasPrefix(low, `c:\windows\`) || strings.Contains(low, `\microsoft\`)
}

// looksLikeGUID 判断字符串是否为 {xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx} 形式的 GUID
func looksLikeGUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 38 || s[0] != '{' || s[len(s)-1] != '}' {
		return false
	}
	for i := 1; i < 37; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F', c == '-':
		default:
			return false
		}
	}
	return true
}

// gradeNamespaceItem 风险分级：关键词规则 → 发布者签名黑名单 → 低风险第三方项
func gradeNamespaceItem(name, display, impl, pub string) (risk, reason string, hit bool) {
	text := name + " " + display + " " + impl
	if rule := matchHighRisk(text); rule != nil {
		return "high", "命中高风险规则「" + rule.Name + "」", true
	}
	if rule := matchRule(text); rule != nil {
		return "medium", "命中疑似推广规则「" + rule.Name + "」", true
	}
	if s := blacklistedSigner(pub); s != "" {
		return "medium", "数字签名者「" + s + "」命中发布者黑名单（sign.txt）", true
	}
	return "low", "", false
}

// namespaceDetail 组装展示用说明。带出签名者，便于用户一眼判断归属
// （例如“签名者: Huorong Security”说明是安全软件自己的右键扩展，不要删）。
func namespaceDetail(reason, impl, pub string) string {
	var b strings.Builder
	if reason != "" {
		b.WriteString(reason)
	} else {
		b.WriteString("非系统自带的外壳扩展项（第三方软件入口）")
	}
	if impl != "" {
		b.WriteString("；实现组件: ")
		b.WriteString(impl)
		if pub != "" {
			b.WriteString("（签名者: ")
			b.WriteString(pub)
			b.WriteString("）")
		}
	} else {
		b.WriteString("；实现组件已不存在（疑似卸载残留）")
	}
	return b.String()
}

// namespaceDisplayName 选择更直观的展示名。
// 命名空间扩展的默认值常是内部标识（CLSID_ThisPC…、@dll,-123），此时用 GUID 更易辨识。
func namespaceDisplayName(display, sub string) string {
	d := strings.TrimSpace(display)
	if d == "" || strings.EqualFold(d, sub) {
		return sub
	}
	up := strings.ToUpper(d)
	if strings.HasPrefix(up, "CLSID_") || strings.HasPrefix(d, "@") {
		return sub
	}
	return d
}
