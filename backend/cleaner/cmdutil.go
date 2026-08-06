package cleaner

import (
	"os/exec"
	"syscall"
)

// hideWindow 返回隐藏控制台窗口的进程属性
func hideWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// runHidden 静默执行命令
func runHidden(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hideWindow()
	return cmd.Run()
}

// runHiddenOutput 静默执行命令并返回输出
func runHiddenOutput(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = hideWindow()
	return cmd.Output()
}
