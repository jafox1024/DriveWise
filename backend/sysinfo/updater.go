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
const (
	// AppVersion 当前应用版本（与 build/config.yml 的 version 保持一致）
	AppVersion = "0.1.0"
	// RepoOwner 版本仓库所有者
	RepoOwner = "jafox1024"
	// RepoName 版本仓库名称
	RepoName = "DriveWise"
	// ReleasePage 版本发布页面
	ReleasePage = "https://github.com/jafox1024/DriveWise/releases"
	// releaseAPI GitHub Releases API（获取最新 release）
	releaseAPI = "https://api.github.com/repos/jafox1024/DriveWise/releases/latest"
)

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
// 返回 UpdateInfo：包含当前/最新版本、是否有更新、发布页链接；失败时 Error 非空。
func (s *SysService) CheckUpdate() models.UpdateInfo {
	info := models.UpdateInfo{
		CurrentVersion: AppVersion,
		ReleaseURL:     ReleasePage,
		CheckedAt:      time.Now().Format("2006-01-02 15:04:05"),
	}

	// GitHub API 需要 User-Agent，否则返回 403
	req, err := http.NewRequest(http.MethodGet, releaseAPI, nil)
	if err != nil {
		info.Error = "构造请求失败: " + err.Error()
		return info
	}
	req.Header.Set("User-Agent", "DriveWise/"+AppVersion)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		info.Error = "网络错误，无法访问 GitHub：请检查网络连接"
		return info
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// 仓库无 release，视为已是最新
		info.LatestVersion = AppVersion
		return info
	}
	if resp.StatusCode != http.StatusOK {
		info.Error = fmt.Sprintf("GitHub 返回异常状态码 %d", resp.StatusCode)
		return info
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		info.Error = "读取响应失败: " + err.Error()
		return info
	}
	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		info.Error = "解析版本信息失败"
		return info
	}

	latest := normalizeVersion(rel.TagName)
	info.LatestVersion = latest
	if rel.HTMLURL != "" {
		info.ReleaseURL = rel.HTMLURL
	}
	if latest != "" && compareVersion(latest, AppVersion) > 0 {
		info.HasUpdate = true
	}
	return info
}

// 版本号规范化：去掉前导 v/V，截取前 3 段数字
var versionRe = regexp.MustCompile(`(\d+(?:\.\d+){0,2})`)

func normalizeVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if m := versionRe.FindStringSubmatch(s); len(m) > 1 {
		return m[1]
	}
	return s
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
