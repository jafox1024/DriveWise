package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupWeChatEnv 将 LOCALAPPDATA 指向临时目录，并构造微信数据目录：
//   - WeChat Files\wxid_a\FileStorage\File\2025-01  （纯空目录树，多级）
//   - WeChat Files\wxid_a\FileStorage\Image\      （含 1 个文件，非空）
//   - WeChat Files\wxid_b\config.ini               （文件）
func setupWeChatEnv(t *testing.T) (emptyDir, nonEmptyDir, file string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)

	emptyDir = filepath.Join(root, `Tencent\WeChat Files\wxid_a\FileStorage\File\2025-01`)
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 更深一层空目录，验证"多级目录为空可删"
	if err := os.MkdirAll(filepath.Join(emptyDir, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	nonEmptyDir = filepath.Join(root, `Tencent\WeChat Files\wxid_a\FileStorage\Image`)
	if err := os.MkdirAll(nonEmptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmptyDir, "photo.dat"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	file = filepath.Join(root, `Tencent\WeChat Files\wxid_b\config.ini`)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	return
}

// TestCleanDataOnlyEmptyDir 数据保护分类：多级空目录允许删除
func TestCleanDataOnlyEmptyDir(t *testing.T) {
	emptyDir, _, _ := setupWeChatEnv(t)
	svc := &CacheService{}

	res := svc.CleanCacheItems("聊天与办公数据", []string{emptyDir})
	if len(res.Errors) != 0 {
		t.Fatalf("空目录应删除成功，实际错误: %v", res.Errors)
	}
	if res.FileCount != 1 {
		t.Fatalf("应删除 1 个目录，实际 %d", res.FileCount)
	}
	if _, err := os.Lstat(emptyDir); !os.IsNotExist(err) {
		t.Fatalf("空目录应已被删除，实际 err=%v", err)
	}
}

// TestCleanDataOnlyRejectsNonEmptyDir 数据保护分类：非空目录禁止删除
func TestCleanDataOnlyRejectsNonEmptyDir(t *testing.T) {
	_, nonEmptyDir, _ := setupWeChatEnv(t)
	svc := &CacheService{}

	res := svc.CleanCacheItems("聊天与办公数据", []string{nonEmptyDir})
	if res.FreedBytes != 0 || res.FileCount != 0 {
		t.Fatalf("非空目录不应删除任何内容")
	}
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "禁止清理") {
		t.Fatalf("应返回「禁止清理」错误，实际: %v", res.Errors)
	}
	if _, err := os.Lstat(nonEmptyDir); err != nil {
		t.Fatalf("非空目录应保留: %v", err)
	}
}

// TestCleanDataOnlyRejectsFile 数据保护分类：文件禁止删除（即使是小文件）
func TestCleanDataOnlyRejectsFile(t *testing.T) {
	_, _, file := setupWeChatEnv(t)
	svc := &CacheService{}

	res := svc.CleanCacheItems("聊天与办公数据", []string{file})
	if res.FreedBytes != 0 || res.FileCount != 0 {
		t.Fatalf("文件不应被删除")
	}
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "文件不可清理") {
		t.Fatalf("应返回「文件不可清理」错误，实际: %v", res.Errors)
	}
	if _, err := os.Lstat(file); err != nil {
		t.Fatalf("文件应保留: %v", err)
	}
}

// TestCleanDataOnlyRejectsOutsideRoot 数据保护分类：分类根之外的路径一律拒绝
func TestCleanDataOnlyRejectsOutsideRoot(t *testing.T) {
	setupWeChatEnv(t)
	svc := &CacheService{}

	outside := filepath.Join(t.TempDir(), "hack")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	res := svc.CleanCacheItems("聊天与办公数据", []string{outside})
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "超出分类范围") {
		t.Fatalf("分类根外路径应拒绝，实际: %v", res.Errors)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("外部目录应保留: %v", err)
	}
}
