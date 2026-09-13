package analyzer

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"unsafe"
)

// TestSHFileOpStructLayout 守住 Win32 SHFILEOPSTRUCTW 的内存布局。
// 结构体一旦被无意改动（字段顺序/类型），P/Invoke 会读到错位的内存并导致
// 静默失败甚至崩溃，这里用固定的 amd64 偏移量做回归保护。
func TestSHFileOpStructLayout(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("布局断言仅针对 amd64")
	}
	if got := unsafe.Sizeof(shFileOpStructW{}); got != 56 {
		t.Fatalf("shFileOpStructW 大小 = %d，期望 56", got)
	}
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"hwnd", unsafe.Offsetof(shFileOpStructW{}.hwnd), 0},
		{"wFunc", unsafe.Offsetof(shFileOpStructW{}.wFunc), 8},
		{"pFrom", unsafe.Offsetof(shFileOpStructW{}.pFrom), 16},
		{"pTo", unsafe.Offsetof(shFileOpStructW{}.pTo), 24},
		{"fFlags", unsafe.Offsetof(shFileOpStructW{}.fFlags), 32},
		{"fAnyOperationsAborted", unsafe.Offsetof(shFileOpStructW{}.fAnyOperationsAborted), 36},
		{"hNameMappings", unsafe.Offsetof(shFileOpStructW{}.hNameMappings), 40},
		{"lpszProgressTitle", unsafe.Offsetof(shFileOpStructW{}.lpszProgressTitle), 48},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s 偏移 = %d，期望 %d", c.name, c.got, c.want)
		}
	}
}

// TestSendToRecycleBinNative 验证原生回收站实现：文件与目录都能被移入回收站。
// 判据与生产代码一致——原路径消失即为成功。测试过程中被删除的对象会进入
// 回收站（不影响断言，且 t.TempDir 的清理对已消失路径是幂等的）。
// 端到端（经由 AnalyzerService.DeletePath）的用例见 fileops_test.go。
func TestSendToRecycleBinNative(t *testing.T) {
	root := t.TempDir()

	file := filepath.Join(root, "recycle-me.txt")
	if err := os.WriteFile(file, []byte("drivewise recycle test"), 0o644); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	if err := sendToRecycleBin(file); err != nil {
		t.Fatalf("文件移入回收站失败: %v", err)
	}
	if _, err := os.Lstat(file); !os.IsNotExist(err) {
		t.Fatalf("文件按预期应已消失，Lstat err=%v", err)
	}

	sub := filepath.Join(root, "recycle-dir")
	if err := os.MkdirAll(filepath.Join(sub, "nested"), 0o755); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "nested", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("准备子文件失败: %v", err)
	}
	if err := sendToRecycleBin(sub); err != nil {
		t.Fatalf("目录移入回收站失败: %v", err)
	}
	if _, err := os.Lstat(sub); !os.IsNotExist(err) {
		t.Fatalf("目录按预期应已消失，Lstat err=%v", err)
	}
}

// TestSendToRecycleBinFromFreshGoroutines 在多个新线程上并发调用，
// 覆盖「goroutine 迁移到未初始化/已初始化 COM 的线程」这一现实场景。
func TestSendToRecycleBinFromFreshGoroutines(t *testing.T) {
	root := t.TempDir()
	const n = 4
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(root, "concurrent-"+strconv.Itoa(i)+".tmp")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("准备文件失败: %v", err)
		}
		paths[i] = p
	}

	done := make(chan error, n)
	for i := 0; i < n; i++ {
		p := paths[i]
		go func() { done <- sendToRecycleBin(p) }()
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Errorf("并发回收站删除失败: %v", err)
		}
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("路径应已消失: %s", p)
		}
	}
}
