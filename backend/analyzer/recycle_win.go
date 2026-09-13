package analyzer

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 把文件/目录移入回收站的原生实现。
//
// 为什么不用 PowerShell（2026-09 改造）：
// 早期实现是 `powershell -Command "Add-Type -AssemblyName Microsoft.VisualBasic;
// [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile(...)"`。该命令行里
// "Add-Type -AssemblyName ..." + "FileSystem::DeleteFile" 这一组合，在国产杀软
// （火绒等）的脚本启发式规则库里与"脚本投放/文件删除型木马"高度同形；加之程序本身
// 未做代码签名，容易被整体判黑。改用 Shell 原生 API 后：
//   - 命令行特征串消失，也不再需要额外拉起 powershell 子进程（更快、无窗口）；
//   - 由 Shell 负责回收站语义（FOF_ALLOWUNDO），行为与资源管理器右键删除一致；
//   - 与 NOCONFIRMATION/SILENT 组合时不弹任何对话框，适合后台批处理。

var (
	procSHFileOperationW = modShell32.NewProc("SHFileOperationW")
)

// shFileOpStructW 对应 Win32 SHFILEOPSTRUCTW，字段顺序与对齐必须与 C 结构体一致：
//
//	HWND(8) UINT(4) [pad4] PCZZWSTR(8) PCZZWSTR(8) WORD(2) [pad2] BOOL(4) LPVOID(8) PCWSTR(8)
type shFileOpStructW struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 uintptr
	pTo                   uintptr
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     uintptr
}

const (
	foDelete          = 0x0003 // FO_DELETE：删除
	fofSilent         = 0x0004 // FOF_SILENT：不显示进度对话框
	fofNoConfirmation = 0x0010 // FOF_NOCONFIRMATION：不弹"确认删除"对话框
	fofAllowUndo      = 0x0040 // FOF_ALLOWUNDO：移入回收站（不加则为永久删除）
	fofNoErrorUI      = 0x0400 // FOF_NOERRORUI：出错不弹框，错误码由返回值给出
)

// sendToRecycleBin 将路径移入回收站。
//
// 成功判据沿用"原路径消失"：某些场景（文件过大放不进回收站、Shell 内部已接管）
// 返回值并不完全可靠，以路径是否仍然存在作为最终事实判断。
func sendToRecycleBin(path string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		lastErr = recycleViaShell(path)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return nil // 文件确已进入回收站
		}
		time.Sleep(150 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("路径仍然存在")
	}
	return fmt.Errorf("移入回收站失败：%w", lastErr)
}

// recycleViaShell 在 STA 线程上调用 SHFileOperationW 执行一次回收站删除。
func recycleViaShell(path string) error {
	// SHFileOperation 要求调用线程处于 STA（单线程套间）；Go 的 goroutine 会在
	// 线程间漂移，因此先锁线程再初始化 COM。若该线程已被其它组件初始化为 MTA，
	// CoInitializeEx 会返回 RPC_E_CHANGED_MODE，此时不能调用 CoUninitialize
	// （并非由本函数初始化），调用本身仍继续尝试。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}

	from, err := windows.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0) // PCZZWSTR 需以双 \0 结尾

	op := shFileOpStructW{
		wFunc:  foDelete,
		pFrom:  uintptr(unsafe.Pointer(&from[0])),
		fFlags: fofSilent | fofNoConfirmation | fofAllowUndo | fofNoErrorUI,
	}
	ret, _, _ := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(from) // 指针仅以 uintptr 存入结构体，GC 看不到引用
	if ret != 0 {
		return fmt.Errorf("Shell 返回错误码 0x%X", uint32(ret))
	}
	if op.fAnyOperationsAborted != 0 {
		return errors.New("Shell 报告操作被中止")
	}
	return nil
}
