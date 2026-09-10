package cleaner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"drivewise/backend/models"
)

// ============ Hosts 劫持条目扫描与清理 ============
//
// 部分流氓/劫持软件把推广域名或钓鱼跳转写进系统 hosts 文件（重定向到广告页）。
// 处理策略：整文件先备份再重写（删除命中劫持特征的行），可一键恢复。
// 解析/消毒逻辑抽成纯函数便于单测；hosts 文件在系统目录，写入需管理员权限。

// hostsFilePath Windows hosts 文件位置
const hostsFilePath = `C:\Windows\System32\drivers\etc\hosts`

// hostsEntry hosts 文件中的一条非注释映射记录
type hostsEntry struct {
	hosts []string // 映射的域名列表
	raw   string   // 原始行（不含行尾换行符）
}

// parseHosts 解析 hosts 内容，返回所有非注释映射记录
func parseHosts(content string) []hostsEntry {
	var out []hostsEntry
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		// 首个字段为 IP，其余为域名
		out = append(out, hostsEntry{hosts: fields[1:], raw: trimmed})
	}
	return out
}

// isHostLineMatch 单行是否命中劫持特征（域名列命中 hijackPatterns）
func isHostLineMatch(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return false
	}
	for _, h := range fields[1:] {
		if matchHijackURL(h) != "" {
			return true
		}
	}
	return false
}

// matchedHostLines 返回命中劫持特征的行与涉及的域名（去重）
func matchedHostLines(content string) (rawLines, domains []string) {
	seen := map[string]bool{}
	for _, e := range parseHosts(content) {
		var hits []string
		for _, h := range e.hosts {
			if matchHijackURL(h) != "" {
				hits = append(hits, h)
				if !seen[h] {
					seen[h] = true
					domains = append(domains, h)
				}
			}
		}
		if len(hits) > 0 {
			rawLines = append(rawLines, e.raw)
		}
	}
	return
}

// sanitizeHosts 删除命中劫持特征的行，返回新内容与被删行数
// 保留原有换行风格（\r\n / \n），并确保结尾有且仅有一个换行
func sanitizeHosts(content string) (string, int) {
	sep := "\n"
	if strings.Contains(content, "\r\n") {
		sep = "\r\n"
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	removed := 0
	for _, ln := range lines {
		base := strings.TrimSuffix(ln, "\r")
		if isHostLineMatch(base) {
			removed++
			continue
		}
		kept = append(kept, ln)
	}
	out := strings.Join(kept, "\n")
	out = strings.TrimRight(out, "\r\n") + sep
	return out, removed
}

// scanHosts 扫描 hosts 文件中的劫持条目（命中即返回单个聚合条目，保证备份/恢复一致性）
func scanHosts() []models.RogueItem {
	content, err := os.ReadFile(hostsFilePath)
	if err != nil {
		return nil
	}
	rawLines, domains := matchedHostLines(string(content))
	if len(rawLines) == 0 {
		return nil
	}
	preview := strings.Join(domains, ", ")
	if len(preview) > 300 {
		preview = preview[:300]
	}
	sample := rawLines
	if len(sample) > 2 {
		sample = sample[:2]
	}
	return []models.RogueItem{{
		ID:        genID("hosts", hostsFilePath, ""),
		Name:      "Hosts 文件劫持条目",
		RuleType:  "hosts",
		Location:  "系统 hosts 文件",
		Path:      hostsFilePath,
		Value:     preview,
		RiskLevel: "medium",
		Detail:    fmt.Sprintf("共 %d 行被写入劫持映射，示例: %s", len(rawLines), strings.Join(sample, " | ")),
		Impact:    "访问的网站被强制重定向到推广/钓鱼地址，影响所有本机程序",
		Action:    "remove",
		Selected:  false,
	}}
}

// backupAndCleanHosts 整文件备份后重写 hosts（删除劫持行），可恢复
func backupAndCleanHosts(item *models.RogueItem, entry *BackupEntry) (*BackupEntry, error) {
	content, err := os.ReadFile(item.Path)
	if err != nil {
		return nil, err
	}
	idDir := filepath.Join(backupRoot(), item.ID)
	if err := os.MkdirAll(idDir, 0o755); err != nil {
		return nil, err
	}
	backed := filepath.Join(idDir, "hosts.bak")
	if err := copyFile(item.Path, backed); err != nil {
		return nil, err
	}
	newContent, removed := sanitizeHosts(string(content))
	if removed == 0 {
		_ = os.Remove(backed)
		return nil, fmt.Errorf("未发现可清理的劫持条目（可能已被处理），请重新扫描")
	}
	if err := os.WriteFile(item.Path, []byte(newContent), 0o644); err != nil {
		_ = os.Remove(backed)
		return nil, err
	}
	entry.OrigPath = item.Path
	entry.BackedPath = backed
	return entry, nil
}
