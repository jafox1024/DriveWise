package analyzer

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"drivewise/backend/internal/fsutil"
	"drivewise/backend/models"
)

const (
	maxConcurrency = 16 // 并发扫描上限
	othersName     = "其他"
)

// scanTree 递归扫描目录树
func scanTree(path string, depth, maxDepth, topN int) (*models.FileNode, error) {
	node := &models.FileNode{Name: filepath.Base(path), Path: path, IsDir: true}
	if path == `\` || path == "/" {
		node.Name = path
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		// 无权限目录：返回空节点但保留自身大小信息
		if info, lerr := os.Lstat(path); lerr == nil {
			node.Size = info.Size()
		}
		return node, nil
	}

	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var sizeMu sync.Mutex
	var totalSize int64
	var children []*models.FileNode

	for _, e := range entries {
		e := e
		full := filepath.Join(path, e.Name())

		// 不跟随符号链接/目录联接，避免环路与重复统计
		if e.Type()&fs.ModeSymlink != 0 {
			if info, ierr := os.Lstat(full); ierr == nil {
				mu.Lock()
				children = append(children, &models.FileNode{Name: e.Name(), Path: full, Size: info.Size(), IsDir: false})
				mu.Unlock()
				sizeMu.Lock()
				totalSize += info.Size()
				sizeMu.Unlock()
			}
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			var child *models.FileNode
			var sz int64

			if e.IsDir() {
				if depth < maxDepth {
					child, _ = scanTree(full, depth+1, maxDepth, topN)
					if child != nil {
						sz = child.Size
					}
				} else {
					// 到达深度上限：不再下钻，但仍需计算目录总大小
					sz, _ = fsutil.DirSize(full, 0)
				}
			} else if info, ierr := os.Lstat(full); ierr == nil {
				sz = info.Size()
				child = &models.FileNode{Name: e.Name(), Path: full, Size: sz, IsDir: false}
			}

			mu.Lock()
			if child == nil && e.IsDir() && depth >= maxDepth {
				child = &models.FileNode{Name: e.Name(), Path: full, Size: sz, IsDir: true}
			}
			if child != nil {
				children = append(children, child)
			}
			mu.Unlock()
			sizeMu.Lock()
			totalSize += sz
			sizeMu.Unlock()
		}()
	}
	wg.Wait()

	// 按大小降序排序，仅保留 TopN 大项，其余聚合为“其他”
	sort.Slice(children, func(i, j int) bool {
		return children[i].Size > children[j].Size
	})

	node.Size = totalSize
	if len(children) > topN {
		var restSize int64
		for _, c := range children[topN:] {
			restSize += c.Size
		}
		trimmed := append([]*models.FileNode(nil), children[:topN]...)
		trimmed = append(trimmed, &models.FileNode{
			Name:  othersName,
			Path:  path + string(os.PathSeparator) + othersName,
			Size:  restSize,
			IsDir: true,
		})
		children = trimmed
	}
	node.Children = children
	return node, nil
}
