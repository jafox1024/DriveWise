package cleaner

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// TestMatchHijackURL 浏览器劫持特征匹配：只命中高置信导航劫持，不误报常见主页
func TestMatchHijackURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://www.hao123.com/?tn=xxx", "hao123"},
		{"http://www.hao123.com", "hao123"},
		{"https://home.qjwm.com/", "qjwm"},
		{"https://www.2345.com/?k1", "2345.com"},
		{"https://www.baidu.com/", ""},                    // 用户自设主页不误报
		{"https://www.bing.com/", ""},                     // 用户自设主页不误报
		{"https://start.qq.com", ""},                      // 非特征站不误报
		{"https://www.google.com/search?q=hao123", "hao123"}, // 配置值含特征即命中（劫持配置常整条 URL 注入）
	}
	for _, c := range cases {
		if got := matchHijackURL(c.in); got != c.want {
			t.Errorf("matchHijackURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseHosts 解析 hosts 内容
func TestParseHosts(t *testing.T) {
	content := "# 127.0.0.1 localhost\n127.0.0.1\tlocalhost\n\n0.0.0.0 hao123.com www.hao123.com\n# 0.0.0.0 ads.example\n127.0.0.1 single.no\n"
	entries := parseHosts(content)
	if len(entries) != 3 {
		t.Fatalf("parseHosts 记录数 = %d, want 3 (%+v)", len(entries), entries)
	}
	if len(entries[1].hosts) != 2 || entries[1].hosts[0] != "hao123.com" {
		t.Errorf("hao123 行解析异常: %+v", entries[1])
	}
	if entries[2].hosts[0] != "single.no" {
		t.Errorf("普通行解析异常: %+v", entries[2])
	}
}

// TestSanitizeHosts 消毒：删除劫持行、保留其它行与注释、CRLF 风格保留
func TestSanitizeHosts(t *testing.T) {
	content := "127.0.0.1\tlocalhost\r\n0.0.0.0 hao123.com\r\n# comment keep me\r\n127.0.0.1 bing.com\r\n0.0.0.0 www.qjwm.com www.2345.com\r\n"
	out, removed := sanitizeHosts(content)
	if removed != 2 {
		t.Fatalf("removed = %d, want 2\nout:\n%s", removed, out)
	}
	if strings.Contains(out, "hao123") || strings.Contains(out, "qjwm") || strings.Contains(out, "2345.com") {
		t.Errorf("劫持域名仍存在:\n%s", out)
	}
	if !strings.Contains(out, "localhost") || !strings.Contains(out, "# comment keep me") || !strings.Contains(out, "bing.com") {
		t.Errorf("合法行被误删:\n%s", out)
	}
	if !strings.Contains(out, "\r\n") {
		t.Errorf("CRLF 换行风格未保留:\n%q", out)
	}
}

// TestAnalyzeShortcutArgs 快捷方式参数注入分析
func TestAnalyzeShortcutArgs(t *testing.T) {
	cases := []struct {
		in        string
		suspCount int
		wantClean string
	}{
		{`--profile-directory=Default https://www.hao123.com/?tn=1`, 1, "--profile-directory=Default"},
		{`www.hao123.com --flag=x`, 1, "--flag=x"},
		{`--load-extension="C:\Program Files\Rogue\ext.dll" --lang=zh-CN`, 1, "--lang=zh-CN"},
		{`--load-extension C:\x\y\ext.dll`, 1, ""},
		{`--app=https://mail.google.com/ --profile-directory=Default`, 0, "--app=https://mail.google.com/ --profile-directory=Default"},
		{`--profile-directory=Default`, 0, "--profile-directory=Default"},
		{``, 0, ""},
	}
	for _, c := range cases {
		susp, clean := analyzeShortcutArgs(c.in)
		if len(susp) != c.suspCount {
			t.Errorf("analyze(%q) susp=%v (%d), want %d 项", c.in, susp, len(susp), c.suspCount)
		}
		if clean != c.wantClean {
			t.Errorf("analyze(%q) clean=%q, want %q", c.in, clean, c.wantClean)
		}
	}
}

// TestSplitArgsQuoted 引号内空格保留
func TestSplitArgsQuoted(t *testing.T) {
	toks := splitArgsQuoted(`--a=1 "C:\dir with space\x.dll" -b`)
	if len(toks) != 3 || toks[1] != `C:\dir with space\x.dll` {
		t.Fatalf("splitArgsQuoted = %v", toks)
	}
}

// TestCollectDirMatches 递归收集：层深限制、命中剪枝、跳过/白名单目录
func TestCollectDirMatches(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(`a`)                     // 普通目录，允许深入
	mk(`a\rogue`)              // 深度2命中
	mk(`a\rogue\nested`)       // 命中目录内部不再收集（剪枝）
	mk(`a\rogue\nested\rogue2`)
	mk(`b\rogue`)              // 深度2命中
	mk(`skipme\rogue`)         // skipme 被 prune，不入内
	mk(`deep1\deep2\deep3\rogue`) // 深度5 超出 maxDepth，不命中

	isMatch := func(name, full string) bool { return name == "rogue" || name == "rogue2" }
	isPrune := func(name, full string) bool { return name == "skipme" }
	budget := 1000
	got := collectDirMatches(root, 3, &budget, isMatch, isPrune)

	want := map[string]bool{
		filepath.Join(root, `a\rogue`):   true,
		filepath.Join(root, `b\rogue`):   true,
	}
	// 剪枝：a\rogue 命中后其内部 rogue2 不应出现
	if strings.Contains(strings.Join(got, "|"), "rogue2") {
		t.Errorf("命中目录内部未被剪枝: %v", got)
	}
	// 跳过目录不入内
	for _, p := range got {
		if strings.Contains(p, "skipme") {
			t.Errorf("prune 目录被扫描: %v", got)
		}
	}
	// 深度限制：5 层不命中
	for _, p := range got {
		if strings.Contains(p, "deep3") {
			t.Errorf("超深目录被命中: %v", got)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("命中数 = %d, want %d: %v", len(got), len(want), got)
	}
	for p := range want {
		found := false
		for _, g := range got {
			if g == p {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("缺少命中: %s (got %v)", p, got)
		}
	}
}

// TestEncodePathsForPS 验证 PowerShell 路径编码往返正确（含空格、中文、特殊字符）
func TestEncodePathsForPS(t *testing.T) {
	paths := []string{
		`C:\Users\jiang\Desktop\Google Chrome.lnk`,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe.lnk`,
		`D:\测试\带空格的目录\桌面.lnk`, // 含中文与空格
		`C:\path\with\"quote".lnk`,                       // 含双引号
		``,                                                // 空路径会被跳过（split 后过滤）
	}
	enc := encodePathsForPS(paths)
	if enc == "" {
		t.Fatal("encode 结果不应为空")
	}
	// 模拟 PowerShell 侧解码：UTF-16LE + Base64 → 按 \r?\n 切分
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 decode 失败: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("UTF-16LE 长度应为偶数: %d", len(raw))
	}
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	decoded := string(utf16.Decode(u16))
	lines := strings.Split(decoded, "\n")
	if len(lines) < 4 {
		t.Fatalf("解码行数过少: got=%d, lines=%v", len(lines), lines)
	}
	for _, wantPath := range paths[:4] {
		if wantPath == "" {
			continue
		}
		var found bool
		for _, ln := range lines {
			if strings.TrimRight(ln, "\r") == wantPath {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("未找到期望路径: %q (lines=%v)", wantPath, lines)
		}
	}
}
