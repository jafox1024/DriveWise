package rules

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// TestNormalizeEncoding UTF-16LE 归一化
func TestNormalizeEncoding(t *testing.T) {
	// 构造 UTF-16LE + BOM
	orig := "\\123coco\\\r\n\\快压\\\r\n"
	u16 := []uint16{0xFEFF} // BOM
	for _, r := range orig {
		u16 = append(u16, uint16(r))
	}
	b := make([]byte, 0, len(u16)*2)
	for _, v := range u16 {
		b = append(b, byte(v), byte(v>>8))
	}
	got := string(normalizeEncoding(b))
	if !strings.Contains(got, "\\快压\\") || !strings.Contains(got, "123coco") {
		t.Errorf("UTF-16 转换失败: %q", got)
	}

	// UTF-8 原样返回
	utf8in := "\ufeff\\abc\\\r\n"
	out := normalizeEncoding([]byte(utf8in))
	if string(out) != "\\abc\\\r\n" {
		t.Errorf("UTF-8 BOM 去除失败: %q", out)
	}
}

// TestCountEntries 条目计数
func TestCountEntries(t *testing.T) {
	lines := []string{"a", "", "b", " ", "c"}
	if n := countEntries(lines); n != 3 {
		t.Errorf("countEntries = %d, want 3", n)
	}
}

// TestBlacklistLoad 数据加载
func TestBlacklistLoad(t *testing.T) {
	if len(blackDirs) < 800 {
		t.Errorf("目录黑名单过少: %d (期望 >800)", len(blackDirs))
	}
	if len(blackSigs) < 300 {
		t.Errorf("签名黑名单过少: %d (期望 >300)", len(blackSigs))
	}
	t.Logf("目录=%d 路径=%d 签名=%d 白名单=%d", len(blackDirs), len(blackPaths), len(blackSigs), len(whiteFiles))
}

// TestBlacklistMatch 匹配逻辑
func TestBlacklistMatch(t *testing.T) {
	if !IsBlacklistedDir("123coco") || !IsBlacklistedDir("快压") {
		t.Error("目录名匹配失败")
	}
	if IsBlacklistedDir("Windows") {
		t.Error("Windows 不应命中")
	}
	if !IsBlacklistedPath(`C:\Users\x\AppData\Local\SoftMgr\a.exe`) {
		t.Error("相对路径匹配失败")
	}
	if !IsBlacklistedPublisher("合肥听风雨网络科技") {
		t.Error("中文签名匹配失败")
	}
	if IsBlacklistedPublisher("Microsoft Corporation") {
		t.Error("Microsoft 不应命中")
	}
	if !IsWhitelistedPath(`C:\x\redis-server.exe`) {
		t.Error("白名单匹配失败")
	}
}

// TestUTF16Roundtrip 辅助：确保构造的 UTF-16 编码正确
func TestUTF16Roundtrip(t *testing.T) {
	s := "你好"
	enc := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(enc)*2)
	for _, v := range enc {
		b = append(b, byte(v), byte(v>>8))
	}
	// 手工解码验证
	var out []uint16
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	if string(utf16.Decode(out)) != s {
		t.Error("UTF-16 roundtrip 失败")
	}
}
