package cleaner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"drivewise/backend/internal/fsutil"
	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
)

// CacheService 缓存清理服务，作为 Wails v3 服务暴露给前端
type CacheService struct{}

// cacheCategoryDef 缓存分类定义（模板）
type cacheCategoryDef struct {
	name         string   // 分类名
	paths        []string // 路径模板，支持 %ENV% 与通配符 *（仅用于 User Data 等目录）
	filePatterns []string // 文件级模式（仅匹配指定文件，如 thumbcache_*.db），优先于 paths
	selected     bool     // 默认选中
	special      bool     // 智能识别型（升级残留等，需专用扫描逻辑）
	admin        bool     // 需管理员权限（系统级目录）
	dataOnly     bool     // 纯数据型（聊天记录/办公文档），禁止清理，仅提示迁移
	migrateHint  string   // 数据保护型分类的迁移建议文案
	desc         string   // 分类说明
}

// defaultCategories 返回内置缓存分类定义
// 参考 DISM++ 空间回收功能分类整理
func defaultCategories() []cacheCategoryDef {
	localAppData := os.Getenv("LOCALAPPDATA")
	appData := os.Getenv("APPDATA")
	temp := os.Getenv("TEMP")

	return []cacheCategoryDef{
		{
			name: "系统临时文件",
			desc: "系统与应用程序产生的临时文件，可安全清理（建议先关闭正在运行的程序）",
			paths: []string{
				temp,
				`C:\Windows\Temp`,
				`C:\Windows\Prefetch`,
			},
			selected: true,
		},
		{
			name: "Windows 更新缓存",
			desc: "Windows 更新下载的安装包与更新日志缓存，清理后不影响已安装的更新",
			paths: []string{
				`C:\Windows\SoftwareDistribution\Download`,
				`C:\Windows\SoftwareDistribution\DataStore\Logs`,
			},
			admin:    true,
			selected: true,
		},
		{
			name: "系统日志与错误报告",
			desc: "Windows 系统日志（CBS/DISM/WER 等）与崩溃转储，日常使用无用，可安全清理",
			paths: []string{
				`C:\Windows\Logs\CBS`,
				`C:\Windows\Logs\DISM`,
				`C:\Windows\System32\LogFiles`,
				`C:\Windows\Minidump`,
				`%PROGRAMDATA%\Microsoft\Windows\WER\ReportArchive`,
				`%PROGRAMDATA%\Microsoft\Windows\WER\ReportQueue`,
			},
			admin:    true,
			selected: false,
		},
		{
			name: "传递优化缓存",
			desc: "Windows 更新加速下载的 Delivery Optimization 缓存，清理后不影响系统，更新会重新下载",
			paths: []string{
				`C:\Windows\SoftwareDistribution\DeliveryOptimization`,
				`%PROGRAMDATA%\Microsoft\Windows\DeliveryOptimization\Cache`,
			},
			admin:    true,
			selected: false,
		},
		{
			name: "浏览器缓存",
			desc: "Chrome/Edge/Firefox 的网页与 GPU 缓存，清理后网页首次访问会稍慢，不影响登录状态",
			paths: []string{
				localAppData + `\Google\Chrome\User Data\*\Cache`,
				localAppData + `\Google\Chrome\User Data\*\Code Cache`,
				localAppData + `\Google\Chrome\User Data\*\GPUCache`,
				localAppData + `\Microsoft\Edge\User Data\*\Cache`,
				localAppData + `\Microsoft\Edge\User Data\*\Code Cache`,
				localAppData + `\Microsoft\Edge\User Data\*\GPUCache`,
				localAppData + `\Mozilla\Firefox\Profiles\*\cache2`,
				localAppData + `\Mozilla\Firefox\Profiles\*\startupCache`,
			},
			selected: true,
		},
		{
			name: "缩略图与图标缓存",
			desc: "文件资源管理器生成的缩略图/图标缓存（thumbcache/iconcache），自动重建，可安全清理",
			filePatterns: []string{
				localAppData + `\Microsoft\Windows\Explorer\thumbcache_*.db`,
				localAppData + `\Microsoft\Windows\Explorer\iconcache_*.db`,
			},
			selected: true,
		},
		{
			name: "字体缓存",
			desc: "系统字体服务缓存，重建后自动重新生成，可安全清理",
			paths: []string{
				`C:\Windows\ServiceProfiles\LocalService\AppData\Local\FontCache`,
				`C:\Windows\System32\FNTCACHE.DAT`,
			},
			admin:    true,
			selected: false,
		},
		{
			name: "开发工具缓存",
			desc: "npm/pnpm/Yarn/pip/Go/JetBrains 等开发工具的包缓存，清理后需要时自动重新下载",
			paths: []string{
				`%LOCALAPPDATA%\npm-cache`,
				`%APPDATA%\npm-cache`,
				`%LOCALAPPDATA%\pnpm-store`,
				`%LOCALAPPDATA%\Yarn\Cache`,
				`%LOCALAPPDATA%\pip\Cache`,
				`%USERPROFILE%\.cache`,
				`%GOCACHE%`,
				`%GOMODCACHE%`,
				`%LOCALAPPDATA%\JetBrains\*\caches`,
			},
			selected: false,
		},
		{
			name: "微软商店与应用缓存",
			desc: "Microsoft Store 与 UWP 应用的临时缓存（INetCache/AC/TempState），清理后应用重新加载",
			paths: []string{
				localAppData + `\Packages\*\AC\INetCache`,
				localAppData + `\Packages\*\AC\Temp`,
				localAppData + `\Packages\*\TempState`,
				localAppData + `\Packages\Microsoft.WindowsStore_*\LocalCache`,
			},
			selected: false,
		},
		{
			name: "崩溃转储文件",
			desc: "程序崩溃时生成的诊断文件，对日常使用无用，可安全清理",
			paths: []string{
				`%LOCALAPPDATA%\CrashDumps`,
				`%LOCALAPPDATA%\CrashReport`,
			},
			selected: false,
		},
		{
			name: "应用临时数据",
			desc: "最近打开文件记录、DirectX 着色器缓存等，清理不影响系统运行",
			paths: []string{
				appData + `\Microsoft\Windows\Recent`,
				localAppData + `\D3DSCache`,
			},
			selected: false,
		},
		{
			name:     "软件升级残留",
			desc:     "WPS/浏览器等软件升级后遗留的旧版本文件与安装包缓存，自动保留最新版本",
			paths: []string{
				`%LOCALAPPDATA%\Kingsoft\WPS Office`,
				`%LOCALAPPDATA%\Google\Chrome\Application`,
				`%LOCALAPPDATA%\Microsoft\Edge\Application`,
				`C:\ProgramData\Package Cache`,
			},
			selected: false,
			special:  true,
		},
		{
			name: "大型应用缓存",
			desc: "VS Code/Teams/Adobe 等大型应用的缓存、日志与崩溃报告，删除后应用会自动重建，可安全清理",
			paths: []string{
				// VS Code
				localAppData + `\Code\Cache`,
				localAppData + `\Code\CachedData`,
				localAppData + `\Code\logs`,
				localAppData + `\Code\Service Worker\CacheStorage`,
				// Teams
				localAppData + `\Microsoft\Teams\Cache`,
				localAppData + `\Microsoft\Teams\Code Cache`,
				localAppData + `\Microsoft\Teams\GPUCache`,
				localAppData + `\Microsoft\Teams\logs`,
				localAppData + `\Microsoft\Teams\CacheStorage`,
				localAppData + `\Microsoft\Teams\Service Worker\CacheStorage`,
				// Adobe 媒体缓存
				appData + `\Adobe\Common\Media Cache`,
				appData + `\Adobe\Common\Media Cache Files`,
				localAppData + `\Adobe\*\Cache`,
				localAppData + `\Adobe\*\logs`,
				// 浏览器 Service Worker / Crashpad / ShaderCache
				localAppData + `\Google\Chrome\User Data\*\Service Worker\CacheStorage`,
				localAppData + `\Google\Chrome\User Data\*\Crashpad`,
				localAppData + `\Google\Chrome\User Data\*\ShaderCache`,
				localAppData + `\Microsoft\Edge\User Data\*\Service Worker\CacheStorage`,
				localAppData + `\Microsoft\Edge\User Data\*\Crashpad`,
				localAppData + `\Microsoft\Edge\User Data\*\ShaderCache`,
				localAppData + `\Mozilla\Firefox\Profiles\*\crashes`,
				// 旧 IE/系统 Internet 缓存
				localAppData + `\Microsoft\Windows\INetCache`,
				// 开发构建缓存
				`%USERPROFILE%\.nuget\plugins`,
				localAppData + `\NuGet\Cache`,
				`%USERPROFILE%\.gradle\caches`,
			},
			selected: true,
		},
		{
			name: "聊天与办公数据",
			desc: "微信/QQ 聊天记录、接收的文件与办公文档。属于个人数据，DriveWise 不会清理，仅提示迁移",
			paths: []string{
				// 微信（旧版与新版 xwechat）
				localAppData + `\Tencent\WeChat Files`,
				localAppData + `\Tencent\xwechat`,
				`%USERPROFILE%\Documents\xwechat_files`,
				// QQ / TIM / 企业微信
				localAppData + `\Tencent\QQ`,
				localAppData + `\Tencent\TIM`,
				`%USERPROFILE%\Documents\Tencent Files`,
				`%USERPROFILE%\Documents\WXWork`,
				// 办公文档：WPS 云文档 / OneDrive / WPS 自动备份
				`%USERPROFILE%\Documents\WPS Cloud Files`,
				`%USERPROFILE%\Documents\WPS Drive`,
				`%USERPROFILE%\OneDrive`,
				appData + `\Kingsoft\office6\backup`,
			},
			selected:    false,
			dataOnly:    true,
			migrateHint: "这些是微信/QQ 聊天记录与办公文档等个人数据，删除将导致数据永久丢失，DriveWise 禁止清理。如需释放 C 盘空间，请前往「软件迁移」页面将数据目录迁移到其他磁盘（会自动建立目录联接，原路径继续可用），或手动把整个目录移动到其他分区。",
		},
	}
}

// resolvePaths 将路径模板解析为实际路径：展开环境变量 + 通配符
func resolvePaths(templates []string) []string {
	var resolved []string
	seen := make(map[string]bool)
	for _, t := range templates {
		t = winutil.ExpandEnv(t)
		if t == "" {
			continue
		}
		if strings.ContainsAny(t, "*?") {
			matches, err := filepath.Glob(t)
			if err == nil {
				for _, m := range matches {
					if !seen[m] {
						seen[m] = true
						resolved = append(resolved, m)
					}
				}
				continue
			}
		}
		if !seen[t] {
			seen[t] = true
			resolved = append(resolved, t)
		}
	}
	return resolved
}

// ScanCache 扫描所有缓存分类，计算各路径占用
func (c *CacheService) ScanCache() []models.CacheCategory {
	categories := make([]models.CacheCategory, 0, len(defaultCategories()))
	for _, def := range defaultCategories() {
		cat := models.CacheCategory{
			Name:        def.name,
			Selected:    def.selected,
			Special:     def.special,
			Admin:       def.admin,
			DataOnly:    def.dataOnly,
			MigrateHint: def.migrateHint,
			Desc:        def.desc,
		}
		switch {
		case def.special:
			// 智能识别型：专用扫描（升级残留）
			details := scanUpgradeResidue()
			for _, d := range details {
				cat.Size += d.Size
				cat.FileCount += d.FileCount
				cat.Paths = append(cat.Paths, d.Path)
			}
			cat.Exists = len(details) > 0
		case len(def.filePatterns) > 0:
			// 文件级模式：匹配指定文件（如 thumbcache_*.db）
			files := resolveFilePatterns(def.filePatterns)
			for _, f := range files {
				info, err := os.Lstat(f)
				if err != nil {
					continue
				}
				cat.Size += info.Size()
				cat.FileCount++
				cat.Paths = append(cat.Paths, f)
			}
			cat.Exists = len(files) > 0
		default:
			paths := resolvePaths(def.paths)
			cat.Paths = paths
			for _, p := range paths {
				res := scanPath(p)
				cat.Size += res.Size
				cat.FileCount += res.FileCount
				if res.Exists {
					cat.Exists = true
				}
			}
		}
		categories = append(categories, cat)
	}
	return categories
}

// CleanCache 清理选中的缓存分类（按分类名匹配）
func (c *CacheService) CleanCache(names []string) []models.CleanResult {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}

	results := make([]models.CleanResult, 0, len(names))
	for _, def := range defaultCategories() {
		if !want[def.name] {
			continue
		}
		// 数据保护型分类：禁止清理（聊天记录/办公文档），仅返回拒绝提示
		if def.dataOnly {
			results = append(results, models.CleanResult{
				Category:   def.name,
				FreedBytes: 0,
				Errors:     []string{def.name + ": 该分类为个人数据（聊天记录/办公文档），禁止清理。请通过「软件迁移」将数据目录迁移到其他磁盘"},
			})
			continue
		}
		switch {
		case def.special:
			// 智能识别型：仅删除旧版本残留（保留最新），Package Cache 等需用户在详情中勾选
			results = append(results, cleanUpgradeResidueOldVersions())
			continue
		case len(def.filePatterns) > 0:
			// 文件级模式：仅删除匹配的缓存文件（如 thumbcache_*.db），不碰目录本身
			files := resolveFilePatterns(def.filePatterns)
			res := models.CleanResult{Category: def.name}
			for _, f := range files {
				freed, err := removePath(f)
				if err != nil {
					res.Errors = append(res.Errors, f+": "+err.Error())
					continue
				}
				res.FreedBytes += freed
				res.FileCount++
			}
			results = append(results, res)
			continue
		}
		paths := resolvePaths(def.paths)
		res := models.CleanResult{Category: def.name}
		for _, p := range paths {
			freed, count, errs := cleanPath(p)
			res.FreedBytes += freed
			res.FileCount += count
			res.Errors = append(res.Errors, errs...)
		}
		results = append(results, res)
	}
	return results
}

// resolveFilePatterns 将文件级模式解析为实际存在的文件列表（展开环境变量 + 通配符）
func resolveFilePatterns(patterns []string) []string {
	var files []string
	seen := make(map[string]bool)
	for _, t := range patterns {
		t = winutil.ExpandEnv(t)
		if t == "" {
			continue
		}
		matches, err := filepath.Glob(t)
		if err != nil {
			continue
		}
		for _, m := range matches {
			if info, ierr := os.Lstat(m); ierr == nil && !info.IsDir() {
				if !seen[m] {
					seen[m] = true
					files = append(files, m)
				}
			}
		}
	}
	return files
}

// scanPath 计算单个路径的占用大小与文件数量
func scanPath(path string) models.PathScanResult {
	res := models.PathScanResult{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		return res
	}
	res.Exists = true
	if !info.IsDir() {
		res.Size = info.Size()
		res.FileCount = 1
		return res
	}
	res.Size, res.FileCount = fsutil.DirSize(path, 0)
	return res
}

// cleanPath 清空指定路径下的所有内容（不删除路径本身），返回释放字节数与失败项
func cleanPath(path string) (int64, int64, []string) {
	var freed int64
	var count int64
	var errs []string

	info, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, path+": "+err.Error())
		}
		return 0, 0, errs
	}

	if !info.IsDir() {
		sz, err := removePath(path)
		if err != nil {
			errs = append(errs, path+": "+err.Error())
			return 0, 0, errs
		}
		return sz, 1, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		errs = append(errs, path+": "+err.Error())
		return 0, 0, errs
	}
	for _, e := range entries {
		full := filepath.Join(path, e.Name())
		// 跳过目录联接，避免删除联接指向的真实数据
		if e.Type()&fs.ModeSymlink != 0 {
			errs = append(errs, full+": 跳过目录联接")
			continue
		}
		sz, err := removePath(full)
		if err != nil {
			errs = append(errs, full+": "+err.Error())
			continue
		}
		freed += sz
		count++
	}
	return freed, count, errs
}

// removePath 删除文件/目录（含只读属性处理），返回释放字节数
func removePath(p string) (int64, error) {
	sz := pathSize(p)
	err := os.RemoveAll(p)
	if err == nil {
		return sz, nil
	}
	// 重试：先清除只读属性（Windows 上 RemoveAll 对只读文件会失败）
	_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, e error) error {
		if e == nil {
			_ = os.Chmod(q, 0o666)
		}
		return nil
	})
	err = os.RemoveAll(p)
	if err != nil {
		return 0, err
	}
	return sz, nil
}

// pathSize 计算单路径占用（目录递归，文件直接取大小）
func pathSize(p string) int64 {
	return fsutil.PathSize(p)
}
