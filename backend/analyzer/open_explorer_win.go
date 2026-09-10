package analyzer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"drivewise/backend/internal/winutil"
)

// 目录/文件在资源管理器中打开（或定位选中）的健壮实现。
//
// 背景（2026-09 实机探针）：在 elevated（管理员）进程里直接 exec 启动 explorer.exe、
// ShellExecuteW("open") 或 explorer /select 三种方式都能弹出新窗口，说明个别场景
// "App 内右键无反应"并非打开方式本身所致（可能被安全软件按调用方策略静默拦截）。
// 因此这里做三重回退 + 逐步诊断日志：
//  1. ShellExecuteW("open", dir)：由 shell 内部处理文件夹打开（主流工具首选）；
//  2. Medium 完整性降权启动 explorer.exe（与桌面实例同完整性）；
//  3. 直接启动 explorer.exe（非管理员环境兜底）。
//
// 每一步的权限、返回值、子进程 PID 均写入 %LOCALAPPDATA%\DriveWise\logs\open.log，
// 实机复现时可直接读日志定位卡点。

const (
	showCmdNormal = 1 // SW_SHOWNORMAL
	maxOpenErr    = 32 // ShellExecuteW 返回值 <=32 视为失败
)

var (
	modShell32        = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteW = modShell32.NewProc("ShellExecuteW")
)

// logOpen 追加一行打开诊断日志（不阻塞、失败静默）。
func logOpen(format string, args ...any) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return
	}
	d := filepath.Join(base, "DriveWise", "logs")
	_ = os.MkdirAll(d, 0o755)
	f, err := os.OpenFile(filepath.Join(d, "open.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s %s\n",
		time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// OpenInExplorer 在资源管理器中打开目录（或定位选中文件）。
func (s *AnalyzerService) OpenInExplorer(path string) error {
	logOpen("== OpenInExplorer path=%q admin=%v", path, winutil.IsAdmin())

	abs, err := filepath.Abs(path)
	if err != nil {
		logOpen("Abs error: %v", err)
		return err
	}
	abs = filepath.Clean(abs)

	info, err := os.Lstat(abs)
	if err != nil {
		logOpen("Lstat error: %v", err)
		return fmt.Errorf("路径不存在或无法访问：%s", abs)
	}
	logOpen("Lstat ok isDir=%v", info.IsDir())

	if !info.IsDir() {
		// 文件：打开父目录并定位选中
		return revealInExplorer(abs)
	}

	// 目录：去掉尾部反斜杠（explorer 对 "C:\dir\" 这类路径处理异常），盘根保持原样
	if !strings.EqualFold(abs, filepath.VolumeName(abs)+`\`) {
		abs = strings.TrimRight(abs, `\/`)
	}

	// 1) ShellExecuteW：shell 直接打开（首选，实机已验证管理员场景下可用，日志 rc=42）
	if rc, serr := shellOpenDir(abs); serr == nil {
		logOpen("ShellExecuteW OK rc=%d", rc)
		return nil
	} else {
		logOpen("ShellExecuteW failed: %v", serr)
	}
	// 2) 兜底：直启 explorer.exe（良性普通启动，启发式权重低）
	//    注：此处刻意不再使用 Medium-IL 的 CreateProcessAsUser 降权回退——
	//    该路径涉及令牌复制/完整性级别改写/以他人身份建进程，是行为启发式
	//    引擎（"注入器/提权/沙箱逃逸"特征）的重度触发点；ShellExecuteW 主路径
	//    已覆盖管理员场景，故降级为此更"良性"的普通启动方式以降低误报面。
	if pid, derr := launchExplorerDirect(abs); derr == nil {
		logOpen("Direct explorer OK pid=%d", pid)
		return nil
	} else {
		logOpen("Direct explorer failed: %v", derr)
		return fmt.Errorf("打开目录失败：%v", derr)
	}
}

// revealInExplorer 打开父目录并选中指定文件
// 仅用 explorer /select 直启（良性），不再走 Medium-IL CreateProcessAsUser 回退。
func revealInExplorer(abs string) error {
	sel := `/select,` + abs
	pid, err := launchExplorerDirect(sel)
	if err != nil {
		logOpen("reveal Direct failed: %v", err)
		return fmt.Errorf("打开所在目录失败：%v", err)
	}
	logOpen("reveal Direct OK pid=%d", pid)
	return nil
}

// shellOpenDir 通过 ShellExecuteW 的 open verb 打开目录，返回原始返回值。
func shellOpenDir(dir string) (uintptr, error) {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return 0, err
	}
	file, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	r1, _, e1 := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, showCmdNormal)
	if uintptr(int32(r1)) <= maxOpenErr {
		if e1 != syscall.Errno(0) {
			return r1, e1
		}
		return r1, fmt.Errorf("ShellExecuteW 返回错误码 %d", int32(r1))
	}
	return r1, nil
}

// launchExplorerDirect 直接以当前进程身份启动 explorer.exe，返回其 PID。
func launchExplorerDirect(args ...string) (uint32, error) {
	cmd := exec.Command("explorer.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// explorer 进程可能很快退出（把请求转发给已有实例），Wait 收尾防僵尸
	go func() { _ = cmd.Wait() }()
	return uint32(cmd.Process.Pid), nil
}

