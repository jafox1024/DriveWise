package sigcheck

import (
	"os"
	"strings"
	"testing"
)

// TestSignerKnownSystemFile 用真实系统文件验证 Authenticode 签名者提取
func TestSignerKnownSystemFile(t *testing.T) {
	candidates := []string{
		`C:\Windows\explorer.exe`,
		`C:\Windows\System32\notepad.exe`,
		`C:\Windows\System32\cmd.exe`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		name, err := Signer(p)
		if err != nil {
			t.Fatalf("Signer(%s) error: %v", p, err)
		}
		if !strings.Contains(name, "Microsoft") {
			t.Fatalf("Signer(%s) = %q, 期望包含 Microsoft", p, name)
		}
		t.Logf("Signer(%s) = %q", p, name)
		return
	}
	t.Skip("未找到已知签名的系统文件")
}

// TestSignerUnsigned 无签名文件应返回错误而非伪结果
func TestSignerUnsigned(t *testing.T) {
	dir := t.TempDir()
	p := dir + `\unsigned.exe`
	if err := os.WriteFile(p, []byte("MZ not really a pe file but unsigned"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 一个没有 PE 结构/签名的文件，不应返回可用的签名者名
	name, err := Signer(p)
	if err == nil && name != "" {
		t.Fatalf("无签名文件不应返回签名者: %q", name)
	}
}
