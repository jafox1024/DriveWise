package sysinfo

import (
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 以管理员身份重新启动自身的原生实现。
//
// 为什么不用 PowerShell（2026-09 改造）：
// 原实现是 `powershell -Command "Start-Process -FilePath '...' -Verb RunAs"`。
// "Start-Process -Verb RunAs" 是国产杀软脚本启发式里与"提权型木马/白利用"高度同形的
// 特征串；且提权动作本身已是行为引擎关注点，再叠加未签名二进制容易整体判黑。
// 改用 shell32 的 ShellExecuteExW + "runas" 谓词——这正是资源管理器
// "以管理员身份运行"菜单项走的同一条路径，属于系统标准提权方式。
//
// 不用 ShellExecuteW 而用 ShellExecuteExW 的原因：需要 SEE_MASK_NOCLOSEPROCESS
// 拿到子进程句柄并及时关闭，避免句柄泄漏；SEE_MASK_NOASYNC 保证本进程即使很快退出，
// 提权请求也已提交完成，不会被中途取消。

var (
	modShell32          = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = modShell32.NewProc("ShellExecuteExW")
)

// shellExecuteInfoW 对应 Win32 SHELLEXECUTEINFOW，
// 字段顺序与对齐必须与 C 结构体完全一致（amd64 下共 112 字节）。
type shellExecuteInfoW struct {
	cbSize         uint32
	fMask          uint32
	hwnd           uintptr
	lpVerb         uintptr
	lpFile         uintptr
	lpParameters   uintptr
	lpDirectory    uintptr
	nShow          int32
	hInstApp       uintptr
	lpIDList       uintptr
	lpClass        uintptr
	hkeyClass      uintptr
	dwHotKey       uint32
	hIconOrMonitor uintptr // union { HICON hIcon; HANDLE hMonitor; }
	hProcess       uintptr
}

const (
	seeMaskNoCloseProcess = 0x00000040 // 成功时返回进程句柄
	seeMaskNoAsync        = 0x00000100 // 同步提交，调用方退出也不影响
	seeMaskFlagNoUI       = 0x00000400 // 失败时不弹系统错误框，由本程序提示
	swShowNormal          = 1          // SW_SHOWNORMAL
)

// runAsElevated 通过 UAC 以管理员权限启动指定可执行文件。
// 用户取消 UAC 时返回 ERROR_CANCELLED (1223)。
func runAsElevated(exe string) error {
	// ShellExecuteEx 要求线程已初始化 COM；goroutine 会漂移，故先锁线程。
	// 若线程已是 MTA（RPC_E_CHANGED_MODE），则不调用 CoUninitialize 并继续。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}

	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return err
	}

	sei := shellExecuteInfoW{
		cbSize:      uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		fMask:       seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:      uintptr(unsafe.Pointer(verb)),
		lpFile:      uintptr(unsafe.Pointer(file)),
		lpDirectory: uintptr(unsafe.Pointer(dir)),
		nShow:       swShowNormal,
	}
	r1, _, e1 := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	runtime.KeepAlive(verb)
	runtime.KeepAlive(file)
	runtime.KeepAlive(dir)

	if sei.hProcess != 0 {
		_ = windows.CloseHandle(windows.Handle(sei.hProcess))
	}
	if r1 == 0 {
		if e1 != syscall.Errno(0) {
			return e1
		}
		return fmt.Errorf("ShellExecuteExW 调用失败")
	}
	return nil
}
