package cleaner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"drivewise/backend/internal/fsutil"
	"drivewise/backend/models"
)

// 系统升级残留候选：Windows 大版本升级 / 安装后遗留的高权限持有目录，
// 通常占用 2~15 GB。所有目录多由 TrustedInstaller 持有，删除前需夺回所有权。
type osUpgradeCandidate struct {
	name  string // 目录基名
	title string // 展示名
}

var osUpgradeCandidates = []osUpgradeCandidate{
	{"$WINDOWS.~BT", "Windows 升级临时文件（升级前备份 / 安装器）"},
	{"$WINDOWS.~WS", "Windows 升级残留（升级后暂存）"},
	{"$WINDOWS.~BS", "Windows 安装临时文件"},
	{"Windows.old", "旧 Windows 系统目录"},
}

// allowedOSUpgradeNames 清理时的白名单（防前端传入任意路径删除）
var allowedOSUpgradeNames = map[string]bool{
	"$WINDOWS.~BT": true,
	"$WINDOWS.~WS": true,
	"$WINDOWS.~BS": true,
	"Windows.old":  true,
}

// systemDriveRoot 返回系统盘根目录（默认 C:\）
func systemDriveRoot() string {
	d := strings.TrimSpace(os.Getenv("SystemDrive"))
	if d == "" {
		d = "C:"
	}
	return strings.TrimRight(d, `\`)
}

// ScanOSUpgradeRemnants 扫描系统盘下的升级残留并统计占用。
// 仅返回真实存在的目录；大小用并发 DirSize 计算，Windows.old 较大时耗时若干秒。
func (c *CacheService) ScanOSUpgradeRemnants() []models.OSUpgradeRemnant {
	root := systemDriveRoot()
	var out []models.OSUpgradeRemnant
	for _, cand := range osUpgradeCandidates {
		p := filepath.Join(root, cand.name)
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		size, count := fsutil.DirSize(p, 0)
		out = append(out, models.OSUpgradeRemnant{
			Name:       cand.title,
			Path:       p,
			Size:       size,
			FileCount:  count,
			NeedsAdmin: true, // 系统区高权限目录，清理需管理员
		})
	}
	return out
}

// CleanOSUpgradeRemnants 清理给定的升级残留路径（白名单校验后夺所有权并删除）。
// 逐目录：先 takeown 夺所有权 → icacls 授权 Administrators 完全控制 → os.RemoveAll。
// 删除失败（文件被占用等）不影响其它目录。
func (c *CacheService) CleanOSUpgradeRemnants(paths []string) []models.OSCleanItemResult {
	nameOf := map[string]string{}
	for _, cand := range osUpgradeCandidates {
		nameOf[filepath.Join(systemDriveRoot(), cand.name)] = cand.title
	}
	var results []models.OSCleanItemResult
	for _, p := range paths {
		res := models.OSCleanItemResult{Path: p, Name: nameOf[p]}
		if res.Name == "" {
			res.Name = filepath.Base(p)
		}
		// 白名单：只允许删除已知的系统升级残留目录
		if !allowedOSUpgradeNames[filepath.Base(p)] {
			res.Error = "该路径不在可清理范围内，已拒绝"
			results = append(results, res)
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			res.Success = false
			res.Error = "目录不存在或已被删除"
			results = append(results, res)
			continue
		}
		// 删除前测量一次占用（Windows.old 较大，避免重复测量），交给 removeSystemDir 删除
		res.Success, res.FreedBytes, res.Error = removeSystemDir(p)
		results = append(results, res)
	}
	return results
}

// removeSystemDir 夺回所有权并删除受 TrustedInstaller 保护的系统目录。
// 返回 (成功与否, 释放字节数, 错误)。夺权步骤均尽力而为，最终用 os.RemoveAll 完成删除。
func removeSystemDir(p string) (bool, int64, string) {
	size, _ := fsutil.DirSize(p, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// 1) 夺所有权（受保护文件需先接管 owner）
	runSysCmd(ctx, "takeown", "/F", `"`+p+`"`, "/R", "/D", "Y")
	// 2) 授权 Administrators (S-1-5-32-560) 完全控制，遍历时忽略单项失败
	runSysCmd(ctx, "icacls", `"`+p+`"`, "/grant", "*S-1-5-32-560:F", "/T", "/C")

	// 3) 删除（os.RemoveAll 会自动剥离只读位；夺权后 TrustedInstaller 权限不再阻塞）
	if err := os.RemoveAll(p); err != nil {
		return false, 0, "删除失败：" + err.Error()
	}
	// 4) 复核：仍存在说明被占用或权限仍不足
	if _, err := os.Lstat(p); err == nil {
		return false, 0, "删除后目录仍存在，可能正被系统占用或未以管理员运行"
	}
	return true, size, ""
}

// runSysCmd 以隐藏窗口运行系统命令并等待结束，忽略退出错误（best-effort）
func runSysCmd(ctx context.Context, name string, args ...string) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
}
