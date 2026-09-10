package cleaner

import (
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
)

// ============ 浏览器主页/启动页/搜索劫持扫描 ============
//
// 流氓/推广软件常通过修改浏览器策略键或 IE 设置，把主页/新标签页/默认搜索
// 锁定到导航广告站。Chrome/Edge 的策略键（Policies）与 IE 的 Main 键均可被
// HKLM/HKCU 写入，其中 HKLM 策略键最顽固（用户无法在浏览器设置里改回）。
//
// 与 Uninstall/目录等通用关键词不同，主页/搜索是“用户可能主动配置”的值
// （例如自设百度主页），因此这里只用高置信劫持特征表 hijackPatterns 匹配，
// 避免把用户自选的百度/必应等常见主页误报。命中项默认不勾选、清理可恢复。

// hijackPatterns 高置信导航劫持特征（小写子串匹配）。
// 仅收录几乎不会由用户主动配置为主页的导航站/劫持中转特征，
// 供 主页值 / Hosts 条目 / 快捷方式参数 三处共用。
var hijackPatterns = []string{
	"hao123",   // hao123.com 全系导航站（劫持高频目标）
	"qjwm",     // 全网加速/导航推广
	"2345.com", // 2345 导航
	"2345导航",
}

// matchHijackURL 值命中劫持特征则返回命中的特征串（未命中返回 ""）
func matchHijackURL(value string) string {
	lower := strings.ToLower(value)
	for _, p := range hijackPatterns {
		if strings.Contains(lower, p) {
			return p
		}
	}
	return ""
}

// browserValueNames 各浏览器策略键中需要检查的值名（只关心存网址/搜索串的）
var browserValueNames = []string{
	"HomepageLocation",             // 主页
	"RestoreOnStartupURLs",         // 启动时打开的网址（多值/字符串）
	"DefaultSearchProviderSearchURL", // 默认搜索引擎
	"NewTabPageLocation",           // 新标签页
}

// browserPolicyTargets Chrome/Edge/IE 主页/搜索相关注册表位置
var browserPolicyTargets = []regTarget{
	{registry.LOCAL_MACHINE, `SOFTWARE\Policies\Google\Chrome`, "Chrome 策略(主页/搜索)"},
	{registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Edge`, "Edge 策略(主页/搜索)"},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Policies\Google\Chrome`, "Chrome 策略(32位,主页/搜索)"},
	{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Policies\Microsoft\Edge`, "Edge 策略(32位,主页/搜索)"},
	{registry.CURRENT_USER, `SOFTWARE\Policies\Google\Chrome`, "Chrome 用户策略(主页/搜索)"},
	{registry.CURRENT_USER, `SOFTWARE\Policies\Microsoft\Edge`, "Edge 用户策略(主页/搜索)"},
	// IE 主页（HKCU 通常为正常用户配置；HKLM 的全机主页被改写基本可判定为劫持）
	{registry.CURRENT_USER, `Software\Microsoft\Internet Explorer\Main`, "IE 主页"},
	{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Internet Explorer\Main`, "IE 主页(全机锁定)"},
}

// browserValueAlias 值名 → 用户可读含义
var browserValueAlias = map[string]string{
	"HomepageLocation":               "主页",
	"RestoreOnStartupURLs":           "启动页网址",
	"DefaultSearchProviderSearchURL": "默认搜索引擎",
	"NewTabPageLocation":             "新标签页",
	"Start Page":                     "主页",
	"Default_Search_URL":             "默认搜索",
}

// readRegString 读取任意常见字符串类注册表值（SZ/EXPAND_SZ/MULTI_SZ），失败返回 ""
// 与 backupAndRemoveRegistry 相同的两阶段读取（探测大小 → 读取原始字节）
func readRegString(k registry.Key, name string) string {
	n, typ, err := k.GetValue(name, nil)
	if err != nil || n == 0 {
		return ""
	}
	data := make([]byte, n)
	if _, _, err := k.GetValue(name, data); err != nil {
		return ""
	}
	switch typ {
	case registry.SZ, registry.EXPAND_SZ:
		return decodeUTF16Bytes(data)
	case registry.MULTI_SZ:
		return strings.Join(splitMultiSZBytes(data), "; ")
	}
	return ""
}

// splitMultiSZBytes 将 REG_MULTI_SZ 原始字节按 \0 分割成多条字符串
func splitMultiSZBytes(data []byte) []string {
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(&data[0])), len(data)/2)
	var out []string
	var cur []uint16
	for _, c := range u {
		if c == 0 {
			if len(cur) > 0 {
				out = append(out, syscall.UTF16ToString(cur))
				cur = nil
			}
		} else {
			cur = append(cur, c)
		}
	}
	if len(cur) > 0 {
		out = append(out, syscall.UTF16ToString(cur))
	}
	return out
}

// scanBrowserHijacks 扫描浏览器主页/搜索劫持
func scanBrowserHijacks() []models.RogueItem {
	var items []models.RogueItem
	for _, t := range browserPolicyTargets {
		hivePrefix := "HKLM"
		if t.hive == registry.CURRENT_USER {
			hivePrefix = "HKCU"
		}
		k, err := registry.OpenKey(t.hive, t.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		// 策略键：只查关注的值名；IE Main 键同名枚举时值名不同，统一按关注集合查
		names := browserValueNames
		if t.hive == registry.CURRENT_USER || strings.Contains(t.path, `Internet Explorer`) {
			names = append(names, "Start Page", "Default_Search_URL")
		}
		for _, name := range names {
			text := readRegString(k, name)
			if text == "" {
				continue
			}
			hit := matchHijackURL(text)
			if hit == "" {
				continue
			}
			alias := browserValueAlias[name]
			if alias == "" {
				alias = name
			}
			short := text
			if len(short) > 300 {
				short = short[:300]
			}
			items = append(items, models.RogueItem{
				ID:        genID("browser", t.path+`\`+name, short),
				Name:      t.desc + "「" + alias + "」被锁定",
				RuleType:  "browser",
				Location:  t.desc,
				Path:      hivePrefix + `\` + t.path + `\` + name,
				Value:     short,
				RiskLevel: "medium",
				Detail:    "被改写为劫持地址: " + short,
				Impact:    "浏览器每次启动/新标签页被强制跳转到导航或广告站，且策略项无法在浏览器设置中改回",
				Action:    "remove",
				Selected:  false,
			})
		}
		k.Close()
	}
	return items
}
