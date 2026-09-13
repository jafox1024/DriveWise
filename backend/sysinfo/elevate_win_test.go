package sysinfo

import (
	"strconv"
	"testing"
	"unsafe"
)

// TestShellExecuteInfoLayout 守住 Win32 SHELLEXECUTEINFOW 的内存布局。
// ShellExecuteExW 通过结构体指针双向传参，字段错位会让提权静默失败。
func TestShellExecuteInfoLayout(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("布局断言仅针对 amd64")
	}
	if got := unsafe.Sizeof(shellExecuteInfoW{}); got != 112 {
		t.Fatalf("shellExecuteInfoW 大小 = %d，期望 112", got)
	}
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"cbSize", unsafe.Offsetof(shellExecuteInfoW{}.cbSize), 0},
		{"fMask", unsafe.Offsetof(shellExecuteInfoW{}.fMask), 4},
		{"hwnd", unsafe.Offsetof(shellExecuteInfoW{}.hwnd), 8},
		{"lpVerb", unsafe.Offsetof(shellExecuteInfoW{}.lpVerb), 16},
		{"lpFile", unsafe.Offsetof(shellExecuteInfoW{}.lpFile), 24},
		{"lpParameters", unsafe.Offsetof(shellExecuteInfoW{}.lpParameters), 32},
		{"lpDirectory", unsafe.Offsetof(shellExecuteInfoW{}.lpDirectory), 40},
		{"nShow", unsafe.Offsetof(shellExecuteInfoW{}.nShow), 48},
		{"hInstApp", unsafe.Offsetof(shellExecuteInfoW{}.hInstApp), 56},
		{"lpIDList", unsafe.Offsetof(shellExecuteInfoW{}.lpIDList), 64},
		{"lpClass", unsafe.Offsetof(shellExecuteInfoW{}.lpClass), 72},
		{"hkeyClass", unsafe.Offsetof(shellExecuteInfoW{}.hkeyClass), 80},
		{"dwHotKey", unsafe.Offsetof(shellExecuteInfoW{}.dwHotKey), 88},
		{"hIconOrMonitor", unsafe.Offsetof(shellExecuteInfoW{}.hIconOrMonitor), 96},
		{"hProcess", unsafe.Offsetof(shellExecuteInfoW{}.hProcess), 104},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s 偏移 = %d，期望 %d", c.name, c.got, c.want)
		}
	}
}
