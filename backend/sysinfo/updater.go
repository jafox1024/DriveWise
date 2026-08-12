package sysinfo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"drivewise/backend/models"
)

// 版本号与更新源配置
// AppVersion 是 var 而非 const：发布构建时由 build/windows/Taskfile.yml 通过
// -ldflags "-X drivewise/backend/sysinfo.AppVersion=<build/config.yml 的 version>" 注入，
// 保证版本号只在 build/config.yml 一处维护。此默认值仅供 go run / go test 兜底。
var AppVersion = "0.1.0"

const (
	// RepoOwner 版本仓库所有者
	RepoOwner = "jafox1024"
	// RepoName 版本仓库名称
	RepoName = "DriveWise"
	// ReleasePage 版本发布页面
	ReleasePage = "https://github.com/jafox1024/DriveWise/releases"
)

// releaseAPI GitHub Releases API（获取最新 release）。var 便于测试注入。
var releaseAPI = "https://api.github.com/repos/jafox1024/DriveWise/releases/latest"

// githubRelease GitHub Releases API 响应（仅提取所需字段）
type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
}

// GetAppVersion 返回当前应用版本号
func (s *SysService) GetAppVersion() string {
	return AppVersion
}

// CheckUpdate 检查 GitHub Releases 是否有新版本。
// 返回 UpdateInfo：包含当前/最新版本、状态（outdated/up-to-date/ahead/error）、发布页链接；
// 仅在网络或解析失败时 Error 非空；本地版本高于线上视为 ahead 状态，不提示异常。
func (s *SysService) CheckUpdate() models.UpdateInfo {
	info := models.UpdateInfo{
		CurrentVersion: AppVersion,
		ReleaseURL:     ReleasePage,
		CheckedAt:      time.Now().Format("2006-01-02 15:04:05"),
	}

	// GitHub API 需要 User-Agent，否则返回 403
	req, err := http.NewRequest(http.MethodGet, releaseAPI, nil)
	if err != nil {
		info.State = "error"
		info.Error = "构造请求失败: " + err.Error()
		return info
	}
	req.Header.Set("User-Agent", "DriveWise/"+AppVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		info.State = "error"
		info.Error = "网络错误，无法访问 GitHub：请检查网络连接"
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// 仓库无 release：线上版本未知，视为已是最新
		info.LatestVersion = AppVersion
		info.State = "up-to-date"
		return info
	}
	if resp.StatusCode != http.StatusOK {
		info.State = "error"
		info.Error = fmt.Sprintf("GitHub 返回异常状态码 %d", resp.StatusCode)
		return info
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		info.State = "error"
		info.Error = "读取响应失败: " + err.Error()
		return info
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		info.State = "error"
		info.Error = "解析版本信息失败"
		return info
	}

	latest := normalizeVersion(rel.TagName)
	info.LatestVersion = latest
	if rel.HTMLURL != "" {
		info.ReleaseURL = rel.HTMLURL
	}
	if latest == "" {
		// 线上版本号无法解析：视为已是最新，不提示异常
		info.State = "up-to-date"
		return info
	}
	switch cmp := compareVersion(latest, AppVersion); {
	case cmp > 0:
		info.HasUpdate = true
		info.State = "outdated"
	case cmp < 0:
		// 本地版本高于线上（如本地构建/预发布版），属正常状态，不提示异常
		info.State = "ahead"
	default:
		info.State = "up-to-date"
	}
	return info
}

// 版本号规范化：去掉前导 v/V，截取开头 3 段数字；无法解析返回空串
var versionRe = regexp.MustCompile(`^(\d+(?:\.\d+){0,2})`)

func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if m := versionRe.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return ""
}

// compareVersion 点分数字版本比较：a>b 返回 1，a<b 返回 -1，相等返回 0
func compareVersion(a, b string) int {
	pa := strings.Split(a, ".")
	pb := strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var va, vb int
		if i < len(pa) {
			va, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			vb, _ = strconv.Atoi(pb[i])
		}
		if va != vb {
			if va > vb {
				return 1
			}
			return -1
		}
	}
	return 0
}
