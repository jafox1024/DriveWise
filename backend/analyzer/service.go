package analyzer

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
)

// AnalyzerService 磁盘分析服务，作为 Wails v3 服务暴露给前端
type AnalyzerService struct{}

// GetDrives 枚举所有可用逻辑磁盘及其容量信息
func (s *AnalyzerService) GetDrives() []models.DriveInfo {
	bitmap, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var drives []models.DriveInfo
	for i := 0; i < 26; i++ {
		if bitmap&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + ":\\"
		di := models.DriveInfo{Name: string(rune('A' + i)) + ":"}

		ptr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		var free, total, totalFree uint64
		if err := windows.GetDiskFreeSpaceEx(ptr, &free, &total, &totalFree); err == nil {
			di.Total = int64(total)
			di.Free = int64(totalFree)
			di.Used = int64(total) - int64(totalFree)
		}
		if di.Total <= 0 {
			continue // 跳过未就绪的盘（如空光驱）
		}
		drives = append(drives, di)
	}
	return drives
}

// ScanDisk 统一磁盘扫描入口（多引擎组合）：
//   - engine=auto：管理员 + NTFS 时优先 MFT 极速扫描，否则回退目录遍历
//   - engine=mft：强制 MFT（失败自动回退遍历并提示）
//   - engine=walk：目录遍历（兼容所有文件系统，无需管理员）
func (s *AnalyzerService) ScanDisk(opts models.ScanOptions) models.DiskScanResult {
	engine := opts.Engine
	if engine == "" {
		engine = "auto"
	}
	drive := strings.TrimSuffix(filepath.VolumeName(opts.Path), ":")
	if drive == "" {
		drive = opts.Path
	}
	if opts.Path == "" {
		drive = "C"
		opts.Path = `C:\`
	}

	if engine == "auto" || engine == "mft" {
		if winutil.IsAdmin() && winutil.IsNTFS(drive+":\\") {
			node, err := s.ScanMFT(drive, opts.TopN)
			if err == nil && node != nil {
				return models.DiskScanResult{Engine: "mft", Node: node}
			}
			if engine == "mft" {
				return models.DiskScanResult{
					Engine:  "walk",
					Message: "MFT 扫描失败，已回退目录遍历：" + err.Error(),
				}
			}
		}
	}

	node, err := s.ScanDirectory(opts)
	res := models.DiskScanResult{Engine: "walk"}
	if err != nil {
		res.Message = err.Error()
	} else {
		res.Node = node
	}
	if engine == "auto" && res.Node != nil {
		res.Message = "当前为非管理员或非 NTFS 卷，已使用目录遍历模式（MFT 需管理员权限）"
	}
	return res
}

// ScanDirectory 扫描指定目录，返回分层文件树（每层仅保留 TopN 大项 + “其他”聚合）
func (s *AnalyzerService) ScanDirectory(opts models.ScanOptions) (*models.FileNode, error) {
	if opts.Path == "" {
		opts.Path = `C:\`
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 3
	}
	if opts.TopN <= 0 {
		opts.TopN = 10
	}
	return scanTree(opts.Path, 0, opts.MaxDepth, opts.TopN)
}
