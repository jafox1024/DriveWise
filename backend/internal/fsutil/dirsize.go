// Package fsutil 提供高性能文件系统遍历工具
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// DirSize 计算目录大小与文件数（并发 BFS worker pool，跳过符号链接）
// workers <= 0 时使用 CPU 核数的 2 倍
// 实现说明：使用原子任务计数替代 WaitGroup 管理动态任务，
// 最后一个处理完任务的 worker 负责关闭队列，避免 Wait/Add 竞态导致漏扫。
func DirSize(root string, workers int) (size int64, count int64) {
	if workers <= 0 {
		workers = runtime.NumCPU() * 2
		if workers < 4 {
			workers = 4
		}
	}

	jobs := make(chan string, 1024)
	var wg sync.WaitGroup
	var pending int64
	atomic.StoreInt64(&pending, 1)
	jobs <- root

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				dir, ok := <-jobs
				if !ok {
					return
				}
				entries, err := os.ReadDir(dir)
				if err == nil {
					for _, e := range entries {
						if e.Type()&fs.ModeSymlink != 0 {
							continue // 不跟随联接，避免环路与重复统计
						}
						full := filepath.Join(dir, e.Name())
						if e.IsDir() {
							atomic.AddInt64(&pending, 1)
							select {
							case jobs <- full:
							default:
								// 队列已满：原地顺序处理，避免阻塞
								atomic.AddInt64(&pending, -1)
								walkDirSeq(full, &size, &count)
							}
						} else if info, ierr := e.Info(); ierr == nil {
							atomic.AddInt64(&size, info.Size())
							atomic.AddInt64(&count, 1)
						}
					}
				}
				if atomic.AddInt64(&pending, -1) == 0 {
					close(jobs)
				}
			}
		}()
	}

	wg.Wait()
	return size, count
}

// walkDirSeq 顺序遍历子目录（队列满时的兜底路径）
func walkDirSeq(root string, size, count *int64) (int64, int64) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			atomic.AddInt64(size, info.Size())
			atomic.AddInt64(count, 1)
		}
		return nil
	})
	return atomic.LoadInt64(size), atomic.LoadInt64(count)
}

// PathSize 计算单个文件/目录占用（文件直接取大小）
func PathSize(p string) int64 {
	info, err := os.Lstat(p)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	sz, _ := DirSize(p, 0)
	return sz
}
