package cleaner

import (
	"strings"
	"testing"
)

// TestDataOnlyCategoryExists 验证「聊天与办公数据」分类存在且标记为 DataOnly
func TestDataOnlyCategoryExists(t *testing.T) {
	svc := &CacheService{}
	cats := svc.ScanCache()

	var found bool
	for _, c := range cats {
		if c.Name == "聊天与办公数据" {
			found = true
			if !c.DataOnly {
				t.Fatalf("分类 %q 应标记 DataOnly=true", c.Name)
			}
			if c.MigrateHint == "" {
				t.Fatalf("分类 %q 应提供迁移建议 MigrateHint", c.Name)
			}
			if c.Selected {
				t.Fatalf("数据保护分类 %q 不应默认勾选", c.Name)
			}
		}
	}
	if !found {
		t.Fatalf("未找到「聊天与办公数据」分类")
	}
}

// TestLargeAppCacheCategoryExists 验证「大型应用缓存」分类存在且可清理
func TestLargeAppCacheCategoryExists(t *testing.T) {
	svc := &CacheService{}
	cats := svc.ScanCache()

	for _, c := range cats {
		if c.Name == "大型应用缓存" {
			if c.DataOnly {
				t.Fatalf("「大型应用缓存」不应是数据保护分类")
			}
			if !c.Selected {
				t.Fatalf("「大型应用缓存」应默认勾选")
			}
			return
		}
	}
	t.Fatalf("未找到「大型应用缓存」分类")
}

// TestCleanCacheRejectsDataOnly CleanCache 必须拒绝数据保护分类，不能删除任何内容
func TestCleanCacheRejectsDataOnly(t *testing.T) {
	svc := &CacheService{}
	results := svc.CleanCache([]string{"聊天与办公数据"})

	if len(results) != 1 {
		t.Fatalf("应返回 1 条结果，实际 %d", len(results))
	}
	r := results[0]
	if r.FreedBytes != 0 || r.FileCount != 0 {
		t.Fatalf("数据保护分类不应释放任何空间: freed=%d count=%d", r.FreedBytes, r.FileCount)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0], "禁止清理") {
		t.Fatalf("应返回「禁止清理」错误，实际: %v", r.Errors)
	}
}

// TestCleanCacheItemsRejectsDataOnly CleanCacheItems 必须拒绝数据保护分类的一切删除
// （假路径会被「超出分类范围」拦截；真实范围内的非空内容会被「禁止清理」拦截）
func TestCleanCacheItemsRejectsDataOnly(t *testing.T) {
	svc := &CacheService{}
	res := svc.CleanCacheItems("聊天与办公数据", []string{`C:\fake\WeChat Files`})

	if res.FreedBytes != 0 || res.FileCount != 0 {
		t.Fatalf("数据保护分类不应删除任何条目")
	}
	if len(res.Errors) == 0 {
		t.Fatalf("应返回拒绝错误，实际无错误")
	}
}
