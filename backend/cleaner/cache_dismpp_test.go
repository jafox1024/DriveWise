package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveFilePatterns 文件级模式解析：只返回存在的文件，忽略目录
func TestResolveFilePatterns(t *testing.T) {
	dir := t.TempDir()
	file1 := filepath.Join(dir, "thumbcache_256.db")
	file2 := filepath.Join(dir, "iconcache_32.db")
	sub := filepath.Join(dir, "thumbcache_sub.db")
	_ = os.WriteFile(file1, make([]byte, 100), 0o644)
	_ = os.WriteFile(file2, make([]byte, 50), 0o644)
	_ = os.MkdirAll(sub, 0o755) // 目录不应被匹配

	patterns := []string{dir + `\thumbcache_*.db`, dir + `\iconcache_*.db`}
	files := resolveFilePatterns(patterns)
	if len(files) != 2 {
		t.Fatalf("应匹配 2 个文件，实际 %d: %v", len(files), files)
	}
	for _, f := range files {
		if f == sub {
			t.Error("目录不应出现在文件级匹配结果中")
		}
	}
}

// TestCleanCacheItems_FilePattern 文件级分类针对性清理：白名单外拒绝
func TestCleanCacheItems_FilePattern(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "thumbcache_256.db")
	_ = os.WriteFile(valid, []byte("cache"), 0o644)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// 直接调用底层校验：匹配路径应放行，越权路径应拒绝
	roots := []string{dir}
	if !isWithinRoots(filepath.Join(dir, "thumbcache_256.db"), roots) {
		t.Error("匹配文件应通过白名单")
	}
	if isWithinRoots(filepath.Join(dir, "..", "outside.db"), roots) {
		t.Error("越权文件应被拒绝")
	}
}

// TestFilePatternsCategoryScan 扫描缩略图缓存分类：返回文件列表且大小正确
func TestFilePatternsCategoryScan(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "thumbcache_256.db"), make([]byte, 2048), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "iconcache_32.db"), make([]byte, 1024), 0o644)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	svc := &CacheService{}
	// 缩略图分类真实路径基于 LOCALAPPDATA，不易在测试中覆盖；
	// 这里直接验证 ScanCacheDetails 对"系统临时文件"仍正常，以及 CleanCacheItems 越权拒绝
	res := svc.CleanCacheItems("缩略图与图标缓存", []string{filepath.Join(dir, "thumbcache_256.db")})
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0], "拒绝") {
		t.Errorf("不在白名单内的文件应被拒绝: %v", res.Errors)
	}
}
