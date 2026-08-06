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
	winsxsMu.Lock()
	defer winsxsMu.Unlock()

	if winsxsStatus.Running {
		return winsxsStatus
	}
	winsxsStarted = time.Now()
	winsxsStatus = models.WinSxSCleanStatus{
		Running:   true,
		StartedAt: winsxsStarted.Format("2006-01-02 15:04:05"),
	}
	go runWinSxSClean()
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

// runWinSxSClean 后台执行 dism 清理并写入状态
func runWinSxSClean() {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dism.exe", "/Online", "/Cleanup-Image", "/StartComponentCleanup")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	text := decodeDismOutput(out)

	winsxsMu.Lock()
	defer winsxsMu.Unlock()
	winsxsStatus.Running = false
	winsxsStatus.Done = true
	winsxsStatus.ElapsedSec = int64(time.Since(winsxsStarted).Seconds())
	winsxsStatus.Output = firstLines(text, 60)
	if err == nil {
		winsxsStatus.Success = true
		winsxsStatus.Message = "组件存储清理完成，Windows 已自动回收可用的磁盘空间"
		return
	}
	msg := strings.TrimSpace(text)
	if msg == "" {
		msg = err.Error()
	}
	winsxsStatus.Success = false
	winsxsStatus.Message = "❌ 清理未成功：" + firstLines(msg, 2)
	if strings.Contains(strings.ToLower(msg), "elevated") || strings.Contains(msg, "管理员") || strings.Contains(msg, "权限") {
		winsxsStatus.Message += "（请以管理员身份运行 DriveWise）"
	}
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
