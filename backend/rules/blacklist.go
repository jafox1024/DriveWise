package rules

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// softcnkiller 黑名单数据（https://gitee.com/softcnkiller/data）
// folder.txt：流氓软件目录名黑名单（\名称\ 或 \相对路径\）
// sign.txt：数字签名/发布者黑名单
// whitepath.txt：白名单文件（避免误伤常见服务）
// 支持通过 UpdateBlacklist 从远端下载覆盖本地（%LOCALAPPDATA%\DriveWise\blacklist\），
// 本地文件存在时优先加载（无需重新编译）。

//go:embed blacklist/folder.txt
var blackFolderData string

//go:embed blacklist/sign.txt
var blackSignData string

//go:embed blacklist/whitepath.txt
var whitePathData string

// 黑名单数据文件
var blacklistFiles = []string{"folder.txt", "sign.txt", "whitepath.txt"}

// 运行时集合
var (
	mu         sync.RWMutex
	blackDirs  map[string]bool // 纯目录名黑名单（小写）
	blackPaths []string        // 带相对路径的黑名单（小写，如 "local\\softmgr"）
	blackSigs  map[string]bool // 发布者黑名单（小写）
	whiteFiles []string        // 白名单文件名（小写）
	localTime  string          // 本地黑名单更新时间（空=内置）
)

func init() {
	buildSets(blackFolderData, blackSignData, whitePathData)
	// 若本地已有更新文件则优先加载
	reloadFromLocal()
}

// LocalDir 返回本地黑名单目录
func LocalDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DriveWise", "blacklist")
}

// buildSets 由数据源构建匹配集合
func buildSets(folderData, signData, whiteData string) {
	newDirs := make(map[string]bool)
	newSigs := make(map[string]bool)
	var newPaths, newWhite []string

	for _, line := range strings.Split(folderData, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := strings.Trim(line, `\`)
		if name == "" {
			continue
		}
		if strings.Contains(name, `\`) {
			newPaths = append(newPaths, strings.ToLower(name))
		} else {
			newDirs[strings.ToLower(name)] = true
		}
	}

	for _, line := range strings.Split(signData, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		newSigs[strings.ToLower(line)] = true
	}

	for _, line := range strings.Split(whiteData, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		newWhite = append(newWhite, strings.ToLower(strings.Trim(line, `\`)))
	}

	mu.Lock()
	blackDirs, blackPaths, blackSigs, whiteFiles = newDirs, newPaths, newSigs, newWhite
	mu.Unlock()
}

// reloadFromLocal 从本地目录加载黑名单（存在且有效时覆盖内置）
func reloadFromLocal() bool {
	dir := LocalDir()
	folder := filepath.Join(dir, "folder.txt")
	sign := filepath.Join(dir, "sign.txt")
	white := filepath.Join(dir, "whitepath.txt")
	fd, err1 := os.ReadFile(folder)
	sd, err2 := os.ReadFile(sign)
	wd, err3 := os.ReadFile(white)
	if err1 != nil || err2 != nil || err3 != nil {
		return false
	}
	if len(strings.Split(string(fd), "\n")) < 500 || len(strings.Split(string(sd), "\n")) < 200 {
		return false // 本地文件异常，回退内置
	}
	buildSets(string(fd), string(sd), string(wd))
	if info, err := os.Stat(folder); err == nil {
		localTime = info.ModTime().Format(time.RFC3339)
	}
	return true
}

// ReloadFromLocal 强制重新加载本地黑名单（更新后调用）
func ReloadFromLocal() bool {
	return reloadFromLocal()
}

// Info 当前黑名单统计信息
func Info() (dirs, paths, signs, white int, localUpdated string) {
	mu.RLock()
	defer mu.RUnlock()
	return len(blackDirs), len(blackPaths), len(blackSigs), len(whiteFiles), localTime
}

// IsBlacklistedDir 判断目录名是否命中黑名单（大小写不敏感）
func IsBlacklistedDir(dirName string) bool {
	mu.RLock()
	ok := blackDirs[strings.ToLower(dirName)]
	mu.RUnlock()
	return ok
}

// IsBlacklistedPath 判断完整路径是否命中黑名单相对路径条目
func IsBlacklistedPath(fullPath string) bool {
	lower := strings.ToLower(fullPath)
	mu.RLock()
	defer mu.RUnlock()
	for _, p := range blackPaths {
		if strings.Contains(lower, `\`+p+`\`) || strings.HasSuffix(lower, `\`+p) {
			return true
		}
	}
	return false
}

// IsBlacklistedPublisher 判断软件发布者/签名是否命中黑名单
func IsBlacklistedPublisher(publisher string) bool {
	mu.RLock()
	ok := blackSigs[strings.ToLower(strings.TrimSpace(publisher))]
	mu.RUnlock()
	return ok
}

// IsWhitelistedPath 判断路径是否命中白名单（常见服务进程，避免误报）
func IsWhitelistedPath(fullPath string) bool {
	lower := strings.ToLower(fullPath)
	mu.RLock()
	defer mu.RUnlock()
	for _, w := range whiteFiles {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}
