package cleaner

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
)

// ============ 浏览器快捷方式参数注入检测与修复 ============
//
// 流氓/推广软件常直接改写桌面/开始菜单里的浏览器快捷方式，在参数中追加：
//  1. 裸 URL（如 "chrome.exe http://www.hao123.com/..."）→ 每次打开都被带上导航页
//  2. --load-extension=<路径> → 强制加载推广扩展（因策略位无法写入时退而求其次）
//
// 检测：读取 .lnk 的 TargetPath + Arguments（WScript.Shell COM），
// 目标为已知浏览器且参数含上述注入特征 → 列为可修复项。
// 修复：先整文件备份 .lnk，再重写 Arguments（删除注入参数），可恢复。
//
// 注意：PWA/网页应用快捷方式使用 --app=<URL> 形式，不属于“裸 URL 注入”，
// 分析逻辑已显式排除，避免误删用户自己创建的网页应用。

// shortcutInfo 单个快捷方式读取结果
type shortcutInfo struct {
	target string // 目标程序完整路径
	args   string // 启动参数原文
}

// browserExeNames 已知浏览器进程名（快捷方式参数注入只对这些目标高置信）
var browserExeNames = map[string]bool{
	"chrome.exe": true, "msedge.exe": true, "firefox.exe": true,
	"iexplore.exe": true, "opera.exe": true, "maxthon.exe": true,
	"360se.exe": true, "360chrome.exe": true, "sogouexplorer.exe": true,
	"liebao.exe": true, "2345explorer.exe": true, "2345chrome.exe": true,
	"baidubrowser.exe": true, "qqbrowser.exe": true, "theworld.exe": true,
	"115br.exe": true,
}

// splitArgsQuoted 按空格切分命令行参数（保留引号内的空格）
func splitArgsQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQ = !inQ
		case (c == ' ' || c == '\t') && !inQ:
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// analyzeShortcutArgs 分析浏览器启动参数，返回可疑注入参数列表与清洗后的参数串。
// 安全放行：--app=<URL>（PWA）、--profile-directory= 等常规标志。
func analyzeShortcutArgs(args string) (suspicious []string, cleaned string) {
	tokens := splitArgsQuoted(args)
	if len(tokens) == 0 {
		return nil, args
	}
	var keep []string
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		low := strings.ToLower(t)
		// `--load-extension <path>` 空格分隔形式：开关与下一个值一并移除
		if low == "--load-extension" {
			if i+1 < len(tokens) {
				suspicious = append(suspicious, t+" "+tokens[i+1])
				i++
			} else {
				suspicious = append(suspicious, t)
			}
			continue
		}
		drop := false
		switch {
		case strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://"):
			drop = true // 裸 URL：正常浏览器快捷方式参数绝不含
		case strings.HasPrefix(low, "--load-extension="),
			strings.HasPrefix(low, "--disable-extensions-except="),
			strings.HasPrefix(low, "-load-extension="):
			drop = true // 强制加载/免禁扩展
		case t != "" && t[0] != '-' && !strings.ContainsAny(t, "=/\\") && strings.Contains(t, "."):
			drop = true // 无协议裸域名（www.xxx.com），正常参数不会出现
		}
		if drop {
			suspicious = append(suspicious, t)
			continue
		}
		keep = append(keep, t)
	}
	return suspicious, strings.Join(keep, " ")
}

// userDesktopDir 解析“用户桌面”真实路径（支持 OneDrive/组策略重定向）
func userDesktopDir() string {
	const shellPath = `Software\Microsoft\Windows\CurrentVersion\Explorer\User Shell Folders`
	if k, err := registry.OpenKey(registry.CURRENT_USER, shellPath, registry.QUERY_VALUE); err == nil {
		v := readRegString(k, "Desktop")
		k.Close()
		if v != "" {
			exp := winutil.ExpandEnv(v)
			if exp != "" {
				if st, err := os.Stat(exp); err == nil && st.IsDir() {
					return exp
				}
			}
		}
	}
	return filepath.Join(os.Getenv("USERPROFILE"), "Desktop")
}

// collectShortcutPaths 收集桌面/公共桌面/开始菜单下的 .lnk（≤4 层）
func collectShortcutPaths() []string {
	var dirs []string
	add := func(p string) {
		if p != "" {
			dirs = append(dirs, p)
		}
	}
	add(userDesktopDir())
	add(`C:\Users\Public\Desktop`)
	add(filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`))
	add(`C:\ProgramData\Microsoft\Windows\Start Menu\Programs`)

	var out []string
	for _, dir := range dirs {
		var walk func(d string, depth int)
		walk = func(d string, depth int) {
			entries, err := os.ReadDir(d)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.IsDir() {
					if depth < 3 {
						walk(filepath.Join(d, e.Name()), depth+1)
					}
					continue
				}
				if strings.HasSuffix(strings.ToLower(e.Name()), ".lnk") {
					out = append(out, filepath.Join(d, e.Name()))
				}
			}
		}
		walk(dir, 0)
	}
	return out
}

// psShortcutRead 一次性读取多个 .lnk 的目标与参数。
//
// 实现要点：**不创建任何临时文件**——早期实现用 os.CreateTemp 在 %TEMP% 生成两个 .txt
// 与 PowerShell 交互，但 `Temp\xxx-NNNNNNNNNN.txt` 这种 pattern 与系统/第三方工具生
// 成的临时文件同形，会被 explorer 的未关联文件 fallback 启发式触发「记事本 - 找不到
// 文件 - 要创建新文件吗？」弹窗（用户截图：dawdlist-2332997088.txt）。
//
// 改为：所有 .lnk 路径 UTF-16LE → Base64 单参数传入；PowerShell 把结果集
// 再 Base64 输出到 stdout，Go 端一次解码后按行切，避免编码问题。
func psShortcutRead(paths []string) map[string]shortcutInfo {
	res := map[string]shortcutInfo{}
	if len(paths) == 0 {
		return res
	}
	inEnc := encodePathsForPS(paths)
	script := "$ErrorActionPreference='SilentlyContinue'; " +
		"$raw=[System.Text.Encoding]::Unicode.GetString([Convert]::FromBase64String($args[0])); " +
		"$paths=$raw -split '\\r?\\n' | Where-Object { $_ -ne '' }; " +
		"$sh=(New-Object -ComObject WScript.Shell); " +
		"$sb=New-Object System.Text.StringBuilder; " +
		"foreach($p in $paths){ try{ " +
		"$s=$sh.CreateShortcut($p); " +
		"[void]$sb.AppendLine(($p + \"`u001f\" + $s.TargetPath + \"`u001f\" + $s.Arguments)) " +
		"}catch{} }; " +
		"$bytes=[System.Text.Encoding]::UTF8.GetBytes($sb.ToString()); " +
		"[Console]::Out.WriteLine([Convert]::ToBase64String($bytes))"
	out, err := runHiddenOutput("powershell", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script, inEnc)
	if err != nil {
		return res
	}
	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return res
	}
	raw, derr := base64.StdEncoding.DecodeString(encoded)
	if derr != nil {
		return res
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\x1f", 3)
		if len(parts) != 3 {
			continue
		}
		res[parts[0]] = shortcutInfo{target: parts[1], args: parts[2]}
	}
	return res
}

// encodePathsForPS 把 .lnk 路径数组合并成一个串并以 UTF-16LE + Base64 编码，
// 作为单个命令行参数传给 PowerShell（避免路径里含空格/引号被 PowerShell 切碎）。
func encodePathsForPS(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	all := make([]byte, 0, 256)
	for i, p := range paths {
		if i > 0 {
			all = append(all, '\r', '\n')
		}
		all = append(all, []byte(p)...)
	}
	// UTF-16LE 每两个字节编码一个字符
	u16 := make([]byte, len(all)*2)
	for i, c := range all {
		u16[2*i] = c
		u16[2*i+1] = 0
	}
	return base64.StdEncoding.EncodeToString(u16)
}

// scanShortcuts 扫描浏览器快捷方式参数注入
func scanShortcuts() []models.RogueItem {
	paths := collectShortcutPaths()
	infos := psShortcutRead(paths)
	var items []models.RogueItem
	for _, p := range paths {
		info, ok := infos[p]
		if !ok {
			continue
		}
		base := strings.ToLower(filepath.Base(info.target))
		suspicious, _ := analyzeShortcutArgs(info.args)
		// 目标为浏览器且仅命中劫持域名（如 --app=hao123 等边界情形）也提示
		if len(suspicious) == 0 && !(browserExeNames[base] && matchHijackURL(info.args) != "") {
			continue
		}
		value := strings.Join(suspicious, " ")
		if value == "" {
			value = info.args
		}
		if len(value) > 300 {
			value = value[:300]
		}
		detail := "目标: " + info.target
		if value != "" {
			detail += "，注入参数: " + value
		}
		items = append(items, models.RogueItem{
			ID:        genID("shortcut", p, value),
			Name:      "浏览器快捷方式被注入参数",
			RuleType:  "shortcut",
			Location:  "快捷方式（浏览器参数注入）",
			Path:      p,
			Value:     value,
			RiskLevel: "medium",
			Detail:    detail,
			Impact:    "每次打开浏览器都会被强加导航页或加载推广扩展，删除快捷方式也无法根治（可能再次注入）",
			Action:    "repair",
			Selected:  false,
		})
	}
	return items
}

// psShortcutRewrite 重写单个快捷方式的启动参数
func psShortcutRewrite(lnkPath, newArgs string) error {
	script := "$ErrorActionPreference='Stop'; " +
		"$s=(New-Object -ComObject WScript.Shell).CreateShortcut($args[0]); " +
		"$s.Arguments=$args[1]; $s.Save()"
	_, err := runHiddenOutput("powershell", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", script, lnkPath, newArgs)
	return err
}

// backupAndRepairShortcut 备份 .lnk 后清除注入参数（repair 动作）
func backupAndRepairShortcut(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	orig := item.Path
	infos := psShortcutRead([]string{orig})
	info, ok := infos[orig]
	if !ok {
		return nil, fmt.Errorf("无法读取快捷方式信息（可能已被删除），请重新扫描")
	}
	suspicious, cleaned := analyzeShortcutArgs(info.args)
	if len(suspicious) == 0 {
		return nil, fmt.Errorf("该快捷方式当前未发现可修复的注入参数（可能已被处理），请重新扫描")
	}
	idDir := filepath.Join(backupRoot(), item.ID)
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		return nil, err
	}
	backed := filepath.Join(idDir, filepath.Base(orig))
	if err := copyFile(orig, backed); err != nil {
		return nil, err
	}
	if err := psShortcutRewrite(orig, cleaned); err != nil {
		_ = os.Remove(backed)
		return nil, fmt.Errorf("重写快捷方式失败: %w", err)
	}
	entry.OrigPath = orig
	entry.BackedPath = backed
	return entry, nil
}
