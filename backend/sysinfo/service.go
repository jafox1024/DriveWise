package sysinfo

import (
	"fmt"
	"os"
	"time"

	"drivewise/backend/internal/winutil"
)

// SysService 系统信息服务，作为 Wails v3 服务暴露给前端
type SysService struct{}

// IsAdmin 检测当前进程是否以管理员权限（elevated）运行
func (s *SysService) IsAdmin() bool {
	return winutil.IsAdmin()
}

// RestartAsAdmin 通过 UAC 提示以管理员身份重新启动本程序（当前实例随后自动退出）
func (s *SysService) RestartAsAdmin() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位程序路径: %w", err)
	}
	// 原生 ShellExecuteExW + "runas" 谓词触发 UAC（见 elevate_win.go）。
	// 刻意不走 powershell "Start-Process -Verb RunAs"：该命令行是杀软脚本启发式
	// 的重点特征串，且额外依赖 powershell 可用性。
	if err := runAsElevated(exe); err != nil {
		return fmt.Errorf("提权启动失败（用户可能取消了 UAC 提示）: %w", err)
	}
	// 延迟退出，确保响应已返回前端
	go func() {
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}
