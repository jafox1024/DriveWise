package rules

import (
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"drivewise/backend/models"
)

// 黑名单远端源（softcnkiller/data 仓库 raw 文件）
const blacklistRemoteBase = "https://gitee.com/softcnkiller/data/raw/master/"

// 各文件最小条目数（低于则视为下载失败/异常，拒绝写入）
var blacklistMinLines = map[string]int{
	"folder.txt":    500,
	"sign.txt":      200,
	"whitepath.txt": 1,
}

// UpdateBlacklist 从远端下载最新黑名单并热加载
func UpdateBlacklist() models.BlacklistUpdateResult {
	result := models.BlacklistUpdateResult{Time: time.Now().Format(time.RFC3339)}
	dir := LocalDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		result.Files = append(result.Files, models.BlacklistFileInfo{Name: "创建目录", Err: err.Error()})
		return result
	}

	client := &http.Client{Timeout: 30 * time.Second}
	updated := false

	for _, name := range blacklistFiles {
		info := models.BlacklistFileInfo{Name: name}
		content, err := downloadBlacklist(client, name)
		if err != nil {
			info.Err = err.Error()
			result.Files = append(result.Files, info)
			continue
		}
		// 校验条目数
		lines := strings.Split(strings.TrimSpace(string(content)), "\n")
		if len(lines) < blacklistMinLines[name] {
			info.Err = fmt.Sprintf("条目数异常（%d），已拒绝写入", len(lines))
			result.Files = append(result.Files, info)
			continue
		}
		info.Count = countEntries(lines)
		// 原子写入：临时文件 + rename
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, content, 0o644); err != nil {
			info.Err = "写入失败: " + err.Error()
			result.Files = append(result.Files, info)
			continue
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			os.Remove(tmp)
			info.Err = "替换失败: " + err.Error()
			result.Files = append(result.Files, info)
			continue
		}
		info.OK = true
		updated = true
		result.Files = append(result.Files, info)
	}

	// 至少一个文件成功则热加载
	if updated {
		reloadFromLocal()
	}
	return result
}

// downloadBlacklist 下载黑名单文件并归一化编码（gitee 存储为 UTF-16LE+BOM）
func downloadBlacklist(client *http.Client, name string) ([]byte, error) {
	resp, err := client.Get(blacklistRemoteBase + name)
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 上限 8MB
	if err != nil {
		return nil, fmt.Errorf("读取失败: %w", err)
	}
	return normalizeEncoding(raw), nil
}

// normalizeEncoding 将 UTF-16LE(BOM) 转为 UTF-8；已是 UTF-8 则原样返回
func normalizeEncoding(b []byte) []byte {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u16 := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u16 = append(u16, binary.LittleEndian.Uint16(b[i:i+2]))
		}
		return []byte(string(utf16.Decode(u16)))
	}
	if len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF {
		// UTF-16BE（罕见）
		u16 := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u16 = append(u16, binary.BigEndian.Uint16(b[i:i+2]))
		}
		return []byte(string(utf16.Decode(u16)))
	}
	// UTF-8 BOM
	return []byte(strings.TrimPrefix(string(b), "\ufeff"))
}

// countEntries 统计有效条目数（去除空行）
func countEntries(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
