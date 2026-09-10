package cleaner

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"drivewise/backend/models"
)

// 中英文 dism 输出解析模式
var (
	reSizeEN      = regexp.MustCompile(`(?i)component store size\s*:\s*([\d.,]+)\s*MB`)
	reReclaimEN   = regexp.MustCompile(`(?i)reclaimable space\s*:\s*([\d.,]+)\s*MB`)
	reLastCleanEN = regexp.MustCompile(`(?i)last cleanup date\s*:\s*([^\r\n]+)`)
	reSizeCN      = regexp.MustCompile(`组件存储大小\s*:\s*([\d.,]+)\s*MB`)
	reReclaimCN   = regexp.MustCompile(`可回收空间\s*:\s*([\d.,]+)\s*MB`)
	reLastCleanCN = regexp.MustCompile(`上次清理操作日期\s*:\s*([^\r\n]+)`)
)

// WinSxS 后台清理状态（进程内单例，前端轮询查询）
var (
	winsxsMu      sync.Mutex
	winsxsStatus  models.WinSxSCleanStatus
	winsxsStarted time.Time
)

// StartWinSxSClean 后台启动 WinSxS 组件存储清理（dism /StartComponentCleanup）。
// 立即返回，实际清理在后台 goroutine 中执行，前端可通过 GetWinSxSStatus 查询进度。
// 需要管理员权限；耗时可能达数十分钟，期间不阻塞界面。
func (c *CacheService) StartWinSxSClean() models.WinSxSCleanStatus {
	return startWinSxSClean(false)
}

// StartWinSxSCleanResetBase 后台启动 WinSxS 激进清理（dism /StartComponentCleanup /ResetBase）。
// 相比标准清理额外删除所有历史版本组件，释放更多空间，但「还原此 Windows 安装 / 组件回滚」将不可用，
// 属破坏性操作——前端须二次确认后调用。状态/轮询/接口与标准清理完全共用。
func (c *CacheService) StartWinSxSCleanResetBase() models.WinSxSCleanStatus {
	return startWinSxSClean(true)
}

// startWinSxSClean 内部通用入口：resetBase 决定是否追加 /ResetBase。
func startWinSxSClean(resetBase bool) models.WinSxSCleanStatus {
	winsxsMu.Lock()
	defer winsxsMu.Unlock()

	if winsxsStatus.Running {
		return winsxsStatus
	}
	winsxsStarted = time.Now()
	winsxsStatus = models.WinSxSCleanStatus{
		Running:   true,
		StartedAt: winsxsStarted.Format("2006-01-02 15:04:05"),
		ResetBase: resetBase,
	}
	go runWinSxSClean(resetBase)
	return winsxsStatus
}

// GetWinSxSStatus 查询 WinSxS 后台清理状态（未启动时返回零值状态）
func (c *CacheService) GetWinSxSStatus() models.WinSxSCleanStatus {
	winsxsMu.Lock()
	defer winsxsMu.Unlock()
	if winsxsStatus.Running {
		winsxsStatus.ElapsedSec = int64(time.Since(winsxsStarted).Seconds())
	}
	return winsxsStatus
}

// runWinSxSClean 后台执行 dism 清理并写入状态。
// 判定成功/失败综合看：退出码 + 输出文本。
// Windows 下 dism 的行为：
//  - 无管理员权限：快速返回，输出 "错误: 740 / 需要提升权限 / Elevation required"。
//  - 权限 OK 且清理成功：输出 "操作成功完成 / The operation completed successfully."。
//    少数情况（清理后需要重启）退出码为 3010，但文本仍含"成功完成"，应视为成功。
//  - 权限 OK 但无组件可回收：输出 "The component cleanup was not successful" 等，视为失败。
func runWinSxSClean(resetBase bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	args := []string{"/Online", "/Cleanup-Image", "/StartComponentCleanup"}
	if resetBase {
		args = append(args, "/ResetBase")
	}
	cmd := exec.CommandContext(ctx, "dism.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(decodeDismOutput(out))

	success, needsAdmin, reason := classifyDism(text, err)

	winsxsMu.Lock()
	defer winsxsMu.Unlock()
	winsxsStatus.Running = false
	winsxsStatus.Done = true
	winsxsStatus.ElapsedSec = int64(time.Since(winsxsStarted).Seconds())
	winsxsStatus.Output = firstLines(text, 60)
	winsxsStatus.Success = success
	switch {
	case success:
		if resetBase {
			winsxsStatus.Message = "✅ 激进清理完成：历史版本组件已删除，Windows 已回收更多磁盘空间（此后组件回滚不可用）"
		} else {
			winsxsStatus.Message = "✅ 组件存储清理完成，Windows 已自动回收可用的磁盘空间"
		}
	case needsAdmin:
		winsxsStatus.Message = "⚠️ 需要管理员权限：DISM /StartComponentCleanup 必须以管理员身份运行。" +
			"请以管理员身份重新启动 DriveWise 再执行本操作。"
		// 附加原始 dism 输出尾部辅助诊断
		if reason != "" {
			winsxsStatus.Message += "（" + firstLines(reason, 2) + "）"
		}
	default:
		winsxsStatus.Message = "❌ 清理未成功：" + firstLines(reason, 3)
	}
}

// classifyDism 综合判断 dism 输出：success / needsAdmin / 失败原因（输出尾部文本）。
// 判定优先级：
//  1. 显式失败码 / 权限不足（740 / elevation / 0x8007xxxx / access denied）→ needsAdmin 或 fail
//  2. 显式成功语（"操作成功" / "completed successfully" / "Cleanup completed") → success
//  3. 都没有时，用 err==nil 兜底（退出码 0 视为成功）
func classifyDism(text string, err error) (success bool, needsAdmin bool, reason string) {
	lower := strings.ToLower(text)
	tail := reasonTail(text)
	if tail == "" {
		tail = err.Error()
	}
	// 1a) 权限不足（最常见：错误 740 / elevation required / 提升权限 / Administrator / access is denied）
	if strings.Contains(text, "错误: 740") ||
		strings.Contains(lower, "error: 740") ||
		strings.Contains(lower, "elevation") ||
		strings.Contains(text, "需要提升权限") ||
		strings.Contains(text, "需要以管理员身份") ||
		strings.Contains(lower, "administrator privileges") ||
		strings.Contains(lower, "access is denied") {
		return false, true, tail
	}
	// 1b) 其它错误码 / 显式失败
	if strings.Contains(lower, "operation did not complete successfully") ||
		strings.Contains(lower, "the operation was not successful") ||
		strings.Contains(lower, "0x8007") ||
		strings.Contains(lower, "component cleanup was not successful") ||
		strings.Contains(text, "操作未成功") ||
		strings.Contains(text, "清理未成功") {
		return false, false, tail
	}
	// 2) 显式成功语
	if strings.Contains(lower, "operation completed successfully") ||
		strings.Contains(lower, "component cleanup was successful") ||
		strings.Contains(text, "操作成功") ||
		strings.Contains(text, "清理成功") ||
		strings.Contains(text, "已成功完成") {
		return true, false, ""
	}
	// 3) 兜底：以退出码为准（退出码 0，或无输出但进程正常退出）
	if err == nil {
		return true, false, ""
	}
	return false, false, tail
}

// reasonTail 返回 dism 输出的最后 3 行（错误信息一般出现在末尾）
func reasonTail(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

// AnalyzeWinSxS 分析 Windows 组件存储（WinSxS），调用 dism /AnalyzeComponentStore
// 注：耗时可达数分钟，建议在后台任务中使用；保留供高级用途
func (c *CacheService) AnalyzeWinSxS() models.WinSxSAnalysis {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	out := runDism(ctx, "/Online", "/Cleanup-Image", "/AnalyzeComponentStore")
	text := string(out)
	return parseWinSxS(text)
}

// runDism 执行 dism 命令（隐藏窗口），返回合并输出（已转 UTF-8）
func runDism(ctx context.Context, args ...string) []byte {
	cmd := exec.CommandContext(ctx, "dism.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// 失败时也保留输出（含权限错误等信息），便于前端提示
		if len(out) == 0 {
			out = []byte(err.Error())
		}
	}
	return []byte(decodeDismOutput(out))
}

// decodeDismOutput 将 dism 原始输出转为 UTF-8：
// 中文 Windows 上 dism 输出为 GBK 编码，直接 string() 会乱码，正则也无法匹配
func decodeDismOutput(out []byte) string {
	if len(out) == 0 {
		return ""
	}
	if utf8.Valid(out) {
		return string(out)
	}
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(out); err == nil {
		return string(decoded)
	}
	return string(out)
}

// parseWinSxS 解析 dism 输出
func parseWinSxS(text string) models.WinSxSAnalysis {
	a := models.WinSxSAnalysis{RawOutput: firstLines(text, 60)}
	a.StoreSize = matchMB(reSizeCN, reSizeEN, text)
	a.ReclaimableSize = matchMB(reReclaimCN, reReclaimEN, text)
	a.LastCleanTime = matchText(reLastCleanCN, reLastCleanEN, text)
	return a
}

// matchMB 依次用中英文正则提取 MB 数值并转换为字节
func matchMB(cn, en *regexp.Regexp, text string) int64 {
	if m := cn.FindStringSubmatch(text); len(m) > 1 {
		return mbToBytes(m[1])
	}
	if m := en.FindStringSubmatch(text); len(m) > 1 {
		return mbToBytes(m[1])
	}
	return 0
}

// matchText 依次用中英文正则提取文本
func matchText(cn, en *regexp.Regexp, text string) string {
	if m := cn.FindStringSubmatch(text); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	if m := en.FindStringSubmatch(text); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// mbToBytes 将 "1,234.5" 形式的 MB 数值解析为字节
func mbToBytes(s string) int64 {
	s = strings.ReplaceAll(s, ",", "")
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(f * 1024 * 1024)
}

// firstLines 截取前 n 行文本
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
