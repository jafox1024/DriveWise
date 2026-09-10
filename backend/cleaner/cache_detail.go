package cleaner

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"drivewise/backend/internal/fsutil"
	"drivewise/backend/models"
)

const detailItemLimit = 200 // 每个路径最多展示的顶层条目数

// ScanCacheDetails 查看指定缓存分类的详细内容（各路径顶层条目，按大小降序）
func (c *CacheService) ScanCacheDetails(categoryName string) []models.CacheDetailPath {
	for _, def := range defaultCategories() {
		if def.name != categoryName {
			continue
		}
		// 智能识别型：返回升级残留专项扫描结果
		if def.special {
			return scanUpgradeResidue()
		}
		// 文件级模式：每个匹配文件作为一条详情
		if len(def.filePatterns) > 0 {
			files := resolveFilePatterns(def.filePatterns)
			details := make([]models.CacheDetailPath, 0, len(files))
			for _, f := range files {
				info, err := os.Lstat(f)
				if err != nil {
					continue
				}
				details = append(details, models.CacheDetailPath{
					Path:      f,
					Size:      info.Size(),
					FileCount: 1,
					Items: []models.CacheDetailItem{{
						Name:    filepath.Base(f),
						Path:    f,
						Size:    info.Size(),
						IsDir:   false,
						ModTime: info.ModTime().Format(time.DateTime),
					}},
				})
			}
			return details
		}
		paths := resolvePaths(def.paths)
		details := make([]models.CacheDetailPath, 0, len(paths))
		for _, p := range paths {
			res := scanPath(p)
			detail := models.CacheDetailPath{
				Path:      p,
				Size:      res.Size,
				FileCount: res.FileCount,
			}
			if res.Exists && res.Size > 0 {
				detail.Items = listTopItems(p, detailItemLimit)
			}
			details = append(details, detail)
		}
		return details
	}
	return nil
}

// CleanCacheItems 针对性清理：删除勾选的具体条目（文件或整个目录）
func (c *CacheService) CleanCacheItems(categoryName string, itemPaths []string) models.CleanResult {
	res := models.CleanResult{Category: categoryName}

	for _, def := range defaultCategories() {
		if def.name != categoryName {
			continue
		}
		// 数据保护型分类：仅允许删除空目录（整棵子树无任何文件），非空内容（聊天记录/办公文档）禁止清理
		if def.dataOnly {
			roots := resolvePaths(def.paths)
			if len(roots) == 0 {
				res.Errors = append(res.Errors, "未找到缓存分类: "+categoryName)
				return res
			}
			for _, p := range itemPaths {
				if !isWithinRoots(p, roots) {
					res.Errors = append(res.Errors, p+": 路径超出分类范围，已拒绝")
					continue
				}
				info, err := os.Lstat(p)
				if err != nil {
					res.Errors = append(res.Errors, p+": "+err.Error())
					continue
				}
				if !info.IsDir() {
					res.Errors = append(res.Errors, p+": 该分类为个人数据（聊天记录/办公文档），仅允许删除空目录，文件不可清理")
					continue
				}
				// 实时校验：目录整棵子树无任何文件（仅空目录层级）才允许删除
				_, count := fsutil.DirSize(p, 0)
				if count > 0 {
					res.Errors = append(res.Errors,
						fmt.Sprintf("%s: 目录内包含 %d 个文件，属个人数据，禁止清理。请先清空内容后再删除空目录，或使用「软件迁移」", p, count))
					continue
				}
				freed, err := removePath(p)
				if err != nil {
					res.Errors = append(res.Errors, p+": "+err.Error())
					continue
				}
				res.FreedBytes += freed
				res.FileCount++
			}
			return res
		}
		// 智能识别型：白名单 = 实时扫描出的残留项路径（精确匹配，保护最新版本）
		if def.special {
			valid := upgradeItemPaths()
			for _, p := range itemPaths {
				if !valid[p] {
					res.Errors = append(res.Errors, p+": 不在可清理的升级残留列表中，已拒绝")
					continue
				}
				freed, err := removePath(p)
				if err != nil {
					res.Errors = append(res.Errors, p+": "+err.Error())
					continue
				}
				res.FreedBytes += freed
				res.FileCount++
			}
			return res
		}
		// 文件级模式：白名单 = 实时匹配出的文件路径（精确匹配，不碰目录本身）
		if len(def.filePatterns) > 0 {
			valid := make(map[string]bool)
			for _, f := range resolveFilePatterns(def.filePatterns) {
				valid[f] = true
			}
			for _, p := range itemPaths {
				if !valid[p] {
					res.Errors = append(res.Errors, p+": 不在可清理的缓存文件列表中，已拒绝")
					continue
				}
				freed, err := removePath(p)
				if err != nil {
					res.Errors = append(res.Errors, p+": "+err.Error())
					continue
				}
				res.FreedBytes += freed
				res.FileCount++
			}
			return res
		}
		// 常规分类：白名单 = 解析路径集合
		roots := resolvePaths(def.paths)
		if len(roots) == 0 {
			res.Errors = append(res.Errors, "未找到缓存分类: "+categoryName)
			return res
		}
		for _, p := range itemPaths {
			if !isWithinRoots(p, roots) {
				res.Errors = append(res.Errors, p+": 路径超出分类范围，已拒绝")
				continue
			}
			freed, err := removePath(p)
			if err != nil {
				res.Errors = append(res.Errors, p+": "+err.Error())
				continue
			}
			res.FreedBytes += freed
			res.FileCount++
		}
		return res
	}
	res.Errors = append(res.Errors, "未找到缓存分类: "+categoryName)
	return res
}

// listTopItems 列出目录顶层条目，按大小降序，最多 limit 个（跳过符号链接）
// 条目大小计算采用并��方式，提升大目录详情加载速度
func listTopItems(dir string, limit int) []models.CacheDetailItem {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	items := make([]models.CacheDetailItem, 0, len(entries))
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if e.Type()&fs.ModeSymlink != 0 {
			continue // 不展示联接，避免误删真实数据
		}
		items = append(items, models.CacheDetailItem{
			Name:  e.Name(),
			Path:  full,
			IsDir: e.IsDir(),
		})
	}

	// 并发计算每个条目的大小与修改时间
	const w = 8
	sem := make(chan struct{}, w)
	var wg sync.WaitGroup
	for i := range items {
		i := i
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			it := &items[i]
			if info, ierr := os.Lstat(it.Path); ierr == nil {
				it.ModTime = info.ModTime().Format(time.DateTime)
				if !it.IsDir {
					it.Size = info.Size()
				}
			}
			if it.IsDir {
				var count int64
				it.Size, count = fsutil.DirSize(it.Path, 0)
				// 目录下无任何文件（仅空目录层级）→ 空壳目录，可安全删除
				it.Empty = count == 0
			}
		}()
	}
	wg.Wait()

	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

// isWithinRoots 校验路径是否位于任一允许根目录之下（禁止删除根本身，大小写不敏感）
func isWithinRoots(p string, roots []string) bool {
	ap, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	ap = strings.TrimRight(ap, `\/`)
	for _, r := range roots {
		ar, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		ar = strings.TrimRight(ar, `\/`)
		if strings.EqualFold(ap, ar) {
			continue // 不允许删除根路径本身
		}
		rel, err := filepath.Rel(ar, ap)
		if err != nil {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
