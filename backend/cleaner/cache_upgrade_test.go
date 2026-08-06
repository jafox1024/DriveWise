package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCompareVersions 版本比较
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"12.1.0.28043", "12.1.0.28042", 1},
		{"12.1.0.28043", "12.1.0.28043", 0},
		{"12.2.0", "12.1.9.999", 1},
		{"138.0.7204.120", "137.0.7151.100", 1},
		{"10.0", "9.9.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%s,%s)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestOldVersionDirs 版本目录识别（保留最新）
func TestOldVersionDirs(t *testing.T) {
	root := t.TempDir()
	// 构造 3 个版本目录 + 1 个非版本目录
	for _, v := range []string{"10.1.0.100", "10.2.0.200", "10.1.5.150", "setup"} {
		_ = os.MkdirAll(filepath.Join(root, v), 0o755)
	}
	// 最新版本里放文件
	_ = os.WriteFile(filepath.Join(root, "10.2.0.200", "app.exe"), []byte("x"), 0o644)

	latest, olds := oldVersionDirs(root)
	if latest != "10.2.0.200" {
		t.Errorf("latest=%s, want 10.2.0.200", latest)
	}
	if len(olds) != 2 {
		t.Fatalf("olds=%d, want 2", len(olds))
	}
	for _, o := range olds {
		if o.Path == filepath.Join(root, "10.2.0.200") {
			t.Error("最新版本不应出现在旧版本列表")
		}
	}
}

// TestScanUpgradeResidue 真实环境扫描不崩溃
func TestScanUpgradeResidue(t *testing.T) {
	details := scanUpgradeResidue()
	t.Logf("升级残留 %d 组", len(details))
	for _, d := range details {
		t.Logf("  - %-30s size=%d items=%d", d.Path, d.Size, len(d.Items))
	}
}

// TestOldPoolDirs 组件池版本解析（保留各组件最新）
func TestOldPoolDirs(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{
		"aspcenter_3.1.0.286", "aspcenter_3.1.0.1210", "aspcenter_3.1.0.1342",
		"kaiwpp_3.1.0.13176", "kaiwpp_3.1.0.13970",
		"singlecomp_3.1.0.999", // 单版本组件：不视为残留
		"notaversiondir",       // 非版本目录：忽略
	} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	olds := oldPoolDirs(root)
	if len(olds) != 3 {
		t.Fatalf("旧版本数=%d, want 3 (aspcenter 2 + kaiwpp 1)", len(olds))
	}
	for _, o := range olds {
		base := filepath.Base(o.Path)
		if strings.Contains(base, "aspcenter_3.1.0.1342") {
			t.Error("aspcenter 最新版不应在旧版本列表")
		}
		if strings.Contains(base, "kaiwpp_3.1.0.13970") {
			t.Error("kaiwpp 最新版不应在旧版本列表")
		}
		if strings.Contains(base, "singlecomp") {
			t.Error("单版本组件不应列出")
		}
		t.Logf("  旧版本: %s", base)
	}
}
