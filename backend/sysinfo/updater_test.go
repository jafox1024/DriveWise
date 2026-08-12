package sysinfo

import "testing"

// TestNormalizeVersion 版本号规范化
func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v0.2.0", "0.2.0"},
		{"V1.2.3", "1.2.3"},
		{"0.2.0", "0.2.0"},
		{"v1.0", "1.0"},
		{"1.2.3-beta.1", "1.2.3"},
		{"v0.1.0 (beta)", "0.1.0"},
	}
	for _, c := range cases {
		if got := normalizeVersion(c.in); got != c.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestCompareVersion 点分数字版本比较
func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.10.0", "0.9.0", 1},  // 不按字符串比较
		{"0.1", "0.1.5", -1},    // 位数不同
		{"1.2.3", "1.2.3.4", -1}, // 多一段视为更大
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
