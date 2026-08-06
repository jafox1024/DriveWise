package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseWinSxS_CN 中文系统 dism 输出解析
func TestParseWinSxS_CN(t *testing.T) {
	text := "部署映像服务和管理工具\n" +
		"组件存储大小 : 5,120.0 MB\n" +
		"可回收空间 : 1,024.5 MB\n" +
		"上次清理操作日期 : 2026-01-15 10:30:00\n" +
		"操作成功完成。"
	a := parseWinSxS(text)
	if a.StoreSize != 5120*1024*1024 {
		t.Errorf("StoreSize = %d, want %d", a.StoreSize, 5120*1024*1024)
	}
	if a.ReclaimableSize != int64(1024.5*1024*1024) {
		t.Errorf("ReclaimableSize = %d", a.ReclaimableSize)
	}
	if a.LastCleanTime != "2026-01-15 10:30:00" {
		t.Errorf("LastCleanTime = %q", a.LastCleanTime)
	}
	if !strings.Contains(a.RawOutput, "组件存储大小") {
		t.Error("RawOutput 应保留原始文本")
	}
}

// TestParseWinSxS_EN 英文系统 dism 输出解析
func TestParseWinSxS_EN(t *testing.T) {
	text := "Component Store Size : 5120.0 MB\nReclaimable Space : 1024.5 MB\nLast Cleanup Date : 2026-01-15 10:30:00"
	a := parseWinSxS(text)
	if a.StoreSize != 5120*1024*1024 {
		t.Errorf("StoreSize = %d", a.StoreSize)
	}
	if a.LastCleanTime != "2026-01-15 10:30:00" {
		t.Errorf("LastCleanTime = %q", a.LastCleanTime)
	}
}

// TestIsWithinRoots 路径白名单校验
func TestIsWithinRoots(t *testing.T) {
	roots := []string{`C:\Users\test\AppData\Local\Temp`}
	cases := []struct {
		path string
		want bool
	}{
		{`C:\Users\test\AppData\Local\Temp\a.tmp`, true},
		{`C:\Users\test\AppData\Local\Temp\sub\b`, true},
		{`c:\users\test\appdata\local\temp\A.TMP`, true}, // 大小写不敏感
		{`C:\Users\test\AppData\Local\Temp`, false},      // 根本身禁止
		{`C:\Windows\System32`, false},                   // 越权
		{`C:\Users\test\AppData\Local\Temp2\x`, false},   // 前缀陷阱
		{`D:\other`, false},
	}
	for _, c := range cases {
		if got := isWithinRoots(c.path, roots); got != c.want {
			t.Errorf("isWithinRoots(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestCleanCacheItems 针对性清理端到端（使用系统临时目录，测完即删）
func TestCleanCacheItems(t *testing.T) {
	temp := os.Getenv("TEMP")
	if temp == "" {
		t.Skip("无 TEMP 环境变量")
	}
	svc := &CacheService{}
	file := filepath.Join(temp, "dwtest_cleanup_a.tmp")
	dir := filepath.Join(temp, "dwtest_cleanup_b")
	_ = os.WriteFile(file, []byte("hello"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "data.bin"), make([]byte, 1024), 0o644)
	t.Cleanup(func() { _ = os.RemoveAll(file); _ = os.RemoveAll(dir) })

	res := svc.CleanCacheItems("系统临时文件", []string{file, dir})
	if res.FileCount != 2 || len(res.Errors) != 0 {
		t.Fatalf("清理结果异常: %+v", res)
	}
	if _, err := os.Lstat(file); err == nil {
		t.Error("文件应已删除")
	}
	if _, err := os.Lstat(dir); err == nil {
		t.Error("目录应已删除")
	}

	// 越权路径必须被拒绝
	res2 := svc.CleanCacheItems("系统临时文件", []string{`C:\Windows\System32\notepad.exe`})
	if len(res2.Errors) == 0 || !strings.Contains(res2.Errors[0], "拒绝") {
		t.Errorf("越权路径应被拒绝: %v", res2.Errors)
	}
}
