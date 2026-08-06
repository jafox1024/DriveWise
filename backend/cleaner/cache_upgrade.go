package cleaner

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"drivewise/backend/internal/fsutil"
	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
)

// 软件升级残留专项扫描：
// 1. WPS Office 多版本目录（保留最新）
// 2. Chrome / Edge / Chromium 多版本目录（保留最新）
// 3. 安装器包缓存（%ProgramData%\Package Cache，VS 等）
// 4. AppData 下 update/updater 相关的大目录（>50MB）

var versionPattern = regexp.MustCompile(`^\d+(\.\d+){1,5}$`)

// poolVersionPattern 匹配 addons pool 目录名：组件名_版本号
var poolVersionPattern = regexp.MustCompile(`^(.*)_(\d+(?:\.\d+){1,3})$`)

// upgradeItem 单个残留项
type upgradeItem struct {
	Path  string
	Size  int64
	Count int64
}

// compareVersions 点分数字版本比较：a>b 返回 1，a<b 返回 -1，相等返回 0
func compareVersions(a, b string) int {
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

// oldVersionDirs 列出根下所有版本子目录，返回最新版本与其余旧版本（含大小）
func oldVersionDirs(root string) (latest string, olds []upgradeItem) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", nil
	}
	var versions []string
	for _, e := range entries {
		if e.IsDir() && versionPattern.MatchString(e.Name()) {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) <= 1 {
		return "", nil
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersions(versions[i], versions[j]) > 0
	})
	latest = versions[0]
	for _, v := range versions[1:] {
		sz, cnt := fsutil.DirSize(filepath.Join(root, v), 0)
		olds = append(olds, upgradeItem{Path: filepath.Join(root, v), Size: sz, Count: cnt})
	}
	return latest, olds
}

// oldPoolDirs 解析 addons pool 目录（组件名_版本号 平铺），
// 对每个组件保留最新版本，返回其余旧版本目录（含大小）
func oldPoolDirs(root string) []upgradeItem {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	groups := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := poolVersionPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		comp, ver := m[1], m[2]
		groups[comp] = append(groups[comp], ver)
	}
	var olds []upgradeItem
	for comp, vers := range groups {
		if len(vers) <= 1 {
			continue // 单版本组件不是残留
		}
		sort.Slice(vers, func(i, j int) bool {
			return compareVersions(vers[i], vers[j]) > 0
		})
		for _, v := range vers[1:] {
			p := filepath.Join(root, comp+"_"+v)
			sz, cnt := fsutil.DirSize(p, 0)
			olds = append(olds, upgradeItem{Path: p, Size: sz, Count: cnt})
		}
	}
	return olds
}

// scanUpgradeResidue 扫描软件升级残留，返回详情列表（每个残留根一个 CacheDetailPath）
func scanUpgradeResidue() []models.CacheDetailPath {
	var details []models.CacheDetailPath

	// 1. WPS Office 多版本
	wpsRoot := winutil.ExpandEnv(`%LOCALAPPDATA%\Kingsoft\WPS Office`)
	if latest, olds := oldVersionDirs(wpsRoot); len(olds) > 0 {
		details = append(details, detailFromUpgradeItems("WPS Office 旧版本", wpsRoot,
			"保留最新版本 "+latest, olds))
	}

	// 1.5 WPS 官方组件缓存池：按组件解析版本，仅清理各组件旧版本（保留最新）
	wpsPool := winutil.ExpandEnv(`%APPDATA%\kingsoft\wps\addons\pool\win-i386`)
	if olds := oldPoolDirs(wpsPool); len(olds) > 0 {
		details = append(details, detailFromUpgradeItems("WPS 组件池旧版本", wpsPool,
			"保留各组件最新版本", olds))
	}

	// 1.6 WPS JS 加载项目录（含用户自定义/离线加载项，删除前请确认）
	wpsJs := winutil.ExpandEnv(`%APPDATA%\kingsoft\wps\jsaddons`)
	if info, err := os.Lstat(wpsJs); err == nil && info.IsDir() {
		sz, cnt := fsutil.DirSize(wpsJs, 0)
		if sz > 1*1024*1024 {
			details = append(details, models.CacheDetailPath{
				Path:      wpsJs,
				Size:      sz,
				FileCount: cnt,
				Items: []models.CacheDetailItem{{
					Name:    "WPS JS 加载项（含自定义内容，删除前请确认）",
					Path:    wpsJs,
					Size:    sz,
					IsDir:   true,
					ModTime: info.ModTime().Format("2006-01-02"),
				}},
			})
		}
	}

	// 2. Chromium 系浏览器多版本
	for _, pair := range [][2]string{
		{`%LOCALAPPDATA%\Google\Chrome\Application`, "Chrome 旧版本"},
		{`%LOCALAPPDATA%\Microsoft\Edge\Application`, "Edge 旧版本"},
		{`%LOCALAPPDATA%\Chromium\Application`, "Chromium 旧版本"},
	} {
		root := winutil.ExpandEnv(pair[0])
		if latest, olds := oldVersionDirs(root); len(olds) > 0 {
			details = append(details, detailFromUpgradeItems(pair[1], root,
				"保留最新版本 "+latest, olds))
		}
	}

	// 3. 安装器包缓存（VS 等，>50MB 才列出）
	pkgCache := `C:\ProgramData\Package Cache`
	if info, err := os.Lstat(pkgCache); err == nil && info.IsDir() {
		sz, cnt := fsutil.DirSize(pkgCache, 0)
		if sz > 50*1024*1024 {
			details = append(details, models.CacheDetailPath{
				Path:      pkgCache,
				Size:      sz,
				FileCount: cnt,
				Items: []models.CacheDetailItem{{
					Name:    "Package Cache（安装器缓存）",
					Path:    pkgCache,
					Size:    sz,
					IsDir:   true,
					ModTime: info.ModTime().Format("2006-01-02"),
				}},
			})
		}
	}

	// 4. AppData 下 update/updater 相关大目录（>50MB）
	for _, root := range []string{
		winutil.ExpandEnv(`%LOCALAPPDATA%`),
		winutil.ExpandEnv(`%APPDATA%`),
	} {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || systemDirName(e.Name()) {
				continue
			}
			lower := strings.ToLower(e.Name())
			if !strings.Contains(lower, "update") && !strings.Contains(lower, "updater") {
				continue
			}
			full := filepath.Join(root, e.Name())
			sz, cnt := fsutil.DirSize(full, 0)
			if sz < 50*1024*1024 {
				continue
			}
			modTime := ""
			if info, ierr := e.Info(); ierr == nil {
				modTime = info.ModTime().Format("2006-01-02")
			}
			details = append(details, models.CacheDetailPath{
				Path:      full,
				Size:      sz,
				FileCount: cnt,
				Items: []models.CacheDetailItem{{
					Name:    e.Name() + "（升级目录）",
					Path:    full,
					Size:    sz,
					IsDir:   true,
					ModTime: modTime,
				}},
			})
		}
	}

	return details
}

// detailFromUpgradeItems 由旧版本项构造详情
func detailFromUpgradeItems(title, root, note string, olds []upgradeItem) models.CacheDetailPath {
	d := models.CacheDetailPath{Path: root}
	for _, o := range olds {
		d.Size += o.Size
		d.FileCount += o.Count
		d.Items = append(d.Items, models.CacheDetailItem{
			Name:    filepath.Base(o.Path),
			Path:    o.Path,
			Size:    o.Size,
			IsDir:   true,
			ModTime: note,
		})
	}
	return d
}

// upgradeItemPaths 实时识别所有可清理残留项的路径集合（白名单）
func upgradeItemPaths() map[string]bool {
	valid := make(map[string]bool)
	for _, d := range scanUpgradeResidue() {
		for _, it := range d.Items {
			valid[it.Path] = true
		}
	}
	return valid
}

// cleanUpgradeResidueOldVersions 整类清理：仅删除旧版本残留（保留最新）
// 覆盖：WPS/Chrome/Edge/Chromium 版本目录旧版 + WPS 组件池旧版；
// Package Cache、jsaddons 与 updater 目录需在详情中勾选删除。
func cleanUpgradeResidueOldVersions() models.CleanResult {
	res := models.CleanResult{Category: "软件升级残留"}
	clean := func(items []upgradeItem) {
		for _, o := range items {
			freed, err := removePath(o.Path)
			if err != nil {
				res.Errors = append(res.Errors, o.Path+": "+err.Error())
				continue
			}
			res.FreedBytes += freed
			res.FileCount++
		}
	}
	// 版本目录类残留
	for _, root := range []string{
		winutil.ExpandEnv(`%LOCALAPPDATA%\Kingsoft\WPS Office`),
		winutil.ExpandEnv(`%LOCALAPPDATA%\Google\Chrome\Application`),
		winutil.ExpandEnv(`%LOCALAPPDATA%\Microsoft\Edge\Application`),
		winutil.ExpandEnv(`%LOCALAPPDATA%\Chromium\Application`),
	} {
		if _, olds := oldVersionDirs(root); len(olds) > 0 {
			clean(olds)
		}
	}
	// WPS 组件池旧版本
	if olds := oldPoolDirs(winutil.ExpandEnv(`%APPDATA%\kingsoft\wps\addons\pool\win-i386`)); len(olds) > 0 {
		clean(olds)
	}
	if len(res.Errors) == 0 && res.FileCount == 0 {
		res.Errors = append(res.Errors, "未发现可清理的旧版本残留")
	}
	return res
}
