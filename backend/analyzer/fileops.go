package analyzer

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"drivewise/backend/internal/fsutil"
)

// 说明：OpenInExplorer 的健壮实现（ShellExecuteW + Medium IL 降权回退）见
// open_explorer_win.go。

// DeletePath 将文件/目录移入回收站（非永久删除）。
// 安全保护：禁止删除磁盘根目录与系统关键目录；返回被删除对象的占用空间。
func (s *AnalyzerService) DeletePath(path string) (int64, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	abs = filepath.Clean(abs)

	if reason, blocked := blockedPath(abs); blocked {
		return 0, errors.New(reason)
	}

	// 计算占用（删除前）
	var size int64
	if info, lerr := os.Lstat(abs); lerr == nil {
		if info.IsDir() {
			size, _ = fsutil.DirSize(abs, 0)
		} else {
			size = info.Size()
		}
	}

	if err := sendToRecycleBin(abs); err != nil {
		return 0, err
	}
	// 使 MFT 缓存中该节点失效并重算祖先大小（磁盘分析页删除后立即刷新）
	mftInvalidatePath(abs)
	return size, nil
}

// blockedPath 检查路径是否受保护，返回禁止原因
func blockedPath(abs string) (string, bool) {
	vol := filepath.VolumeName(abs) // 如 "C:"
	// 磁盘根目录
	if strings.EqualFold(filepath.Clean(abs), vol+`\`) {
		return "禁止删除磁盘根目录", true
	}
	// 系统关键目录（Windows/Program Files/Users 等根级系统目录）
	protected := []string{
		`\Windows`, `\Program Files`, `\Program Files (x86)`, `\ProgramData`,
		`\Users`, `\$Recycle.Bin`, `\Recovery`, `\System Volume Information`,
	}
	for _, p := range protected {
		if strings.EqualFold(filepath.Clean(abs), vol+p) {
			return "禁止删除系统关键目录: " + vol + p, true
		}
	}
	return "", false
}

// sendToRecycleBin 将路径移入回收站（通过 PowerShell Microsoft.VisualBasic API）
// 注：PowerShell 5.1 的 DeleteFile 删除成功后可能误报“找不到文件”，
// 以原路径是否消失作为成功判据（文件确实已进入回收站）。
func sendToRecycleBin(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	method := "DeleteFile"
	if info.IsDir() {
		method = "DeleteDirectory"
	}
	escaped := strings.ReplaceAll(path, "'", "''")
	ps := fmt.Sprintf(
		`Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.FileIO.FileSystem]::%s('%s','OnlyErrorDialogs','SendToRecycleBin')`,
		method, escaped,
	)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_, _ = cmd.CombinedOutput()

	// 验证：原路径已不存在 = 成功进入回收站
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("移入回收站失败，路径仍存在: %s", path)
}
