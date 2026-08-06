package models

// CacheCategory 缓存分类，用于前端展示与选择
type CacheCategory struct {
	Name      string   `json:"name"`      // 分类名称
	Paths     []string `json:"paths"`     // 该分类包含的缓存路径（已解析环境变量）
	Size      int64    `json:"size"`      // 占用空间（字节）
	FileCount int64    `json:"fileCount"` // 文件数量
	Selected  bool     `json:"selected"`  // 默认是否选中
	Exists    bool     `json:"exists"`    // 路径是否存在（存在才可扫描/清理）
	Special   bool     `json:"special"`   // 是否智能识别型分类（升级残留等，需专用扫描）
	Desc      string   `json:"desc"`      // 分类说明
}

// CleanResult 单分类清理结果
type CleanResult struct {
	Category   string   `json:"category"`   // 分类名称
	FreedBytes int64    `json:"freedBytes"` // 释放空间（字节）
	FileCount  int64    `json:"fileCount"`  // 成功删除的文件/目录数
	Errors     []string `json:"errors"`     // 失败项说明
}

// PathScanResult 单路径扫描结果（内部聚合用）
type PathScanResult struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	FileCount int64  `json:"fileCount"`
	Exists    bool   `json:"exists"`
}

// CacheDetailItem 缓存目录下的单个条目（文件或目录）
type CacheDetailItem struct {
	Name    string `json:"name"`    // 名称
	Path    string `json:"path"`    // 完整路径
	Size    int64  `json:"size"`    // 占用（目录为递归总大小）
	IsDir   bool   `json:"isDir"`   // 是否目录
	ModTime string `json:"modTime"` // 修改时间
}

// CacheDetailPath 单个缓存路径的详情
type CacheDetailPath struct {
	Path      string            `json:"path"`      // 缓存路径
	Size      int64             `json:"size"`      // 路径总占用
	FileCount int64             `json:"fileCount"` // 文件数
	Items     []CacheDetailItem `json:"items"`     // 顶层条目（前 N 大）
}

// WinSxSAnalysis WinSxS 组件存储分析结果
type WinSxSAnalysis struct {
	StoreSize       int64  `json:"storeSize"`       // 组件存储大小（字节）
	ReclaimableSize int64  `json:"reclaimableSize"` // 可回收空间（字节）
	LastCleanTime   string `json:"lastCleanTime"`   // 上次清理时间
	RawOutput       string `json:"rawOutput"`       // 原始输出（前若干行）
}

// WinSxSCleanStatus WinSxS 后台清理状态（StartWinSxSClean / GetWinSxSStatus）
type WinSxSCleanStatus struct {
	Running    bool   `json:"running"`    // 是否正在后台清理
	Done       bool   `json:"done"`       // 是否已完成（无论成败）
	Success    bool   `json:"success"`    // 是否成功
	Message    string `json:"message"`    // 结果/错误提示
	Output     string `json:"output"`     // dism 原始输出（前若干行）
	StartedAt  string `json:"startedAt"`  // 开始时间（2006-01-02 15:04:05）
	ElapsedSec int64  `json:"elapsedSec"` // 已耗时（秒）
}

// DriveInfo 逻辑磁盘信息
type DriveInfo struct {
	Name  string `json:"name"`  // 盘符，如 C:
	Total int64  `json:"total"` // 总容量（字节）
	Used  int64  `json:"used"`  // 已用（字节）
	Free  int64  `json:"free"`  // 可用（字节）
}

// FileNode 文件树节点
type FileNode struct {
	Name     string      `json:"name"`                // 名称
	Path     string      `json:"path"`                // 完整路径
	Size     int64       `json:"size"`                // 占用（字节）
	IsDir    bool        `json:"isDir"`               // 是否目录
	Children []*FileNode `json:"children,omitempty"`  // 子节点（仅目录）
}

// ScanOptions 磁盘扫描参数
type ScanOptions struct {
	Path     string `json:"path"`     // 扫描根路径
	MaxDepth int    `json:"maxDepth"` // 最大深度（0 表示默认 3）
	TopN     int    `json:"topN"`     // 每层保留的最大子节点数（0 表示默认 10）
	Engine   string `json:"engine"`   // 扫描引擎: auto | mft | walk
}

// DiskScanResult 磁盘扫描结果（多引擎组合）
type DiskScanResult struct {
	Engine   string    `json:"engine"`   // 实际使用的引擎: mft | walk
	Node     *FileNode `json:"node"`     // 文件树（截断版）
	Duration int64     `json:"duration"` // 耗时（毫秒）
	Message  string    `json:"message"`  // 提示（如引擎回退说明）
}

// MigratableApp 可迁移的应用目录
type MigratableApp struct {
	Name      string `json:"name"`      // 应用名
	Path      string `json:"path"`      // 当前路径
	Size      int64  `json:"size"`      // 占用空间
	FileCount int64  `json:"fileCount"` // 文件数
	Target    string `json:"target"`    // 建议迁移目标
}

// RogueItem 流氓软件扫描结果项
type RogueItem struct {
	ID        string `json:"id"`        // 唯一 ID（用于清理）
	Name      string `json:"name"`      // 软件名
	RuleType  string `json:"ruleType"`  // 匹配类型: registry | file | service | task | extension | installed | startup | process | shellex
	Location  string `json:"location"`  // 位置描述
	Path      string `json:"path"`      // 具体路径/注册表值/服务名/任务名
	Value     string `json:"value"`     // 注册表值数据/显示名（如有）
	RiskLevel string `json:"riskLevel"` // high | medium | low
	Detail    string `json:"detail"`    // 说明
	Impact    string `json:"impact"`    // 影响说明（用户可感知的行为）
	Action    string `json:"action"`    // 处理方式: remove | disable_service | disable_task | remove_extension | hint
	Selected  bool   `json:"selected"`  // 是否选中清理
	Running   bool   `json:"running"`   // 相关进程是否正在运行（需先终止）
	PID       int    `json:"pid"`       // 相关进程 PID（如有）
}

// RogueCleanResult 清理结果
type RogueCleanResult struct {
	ID     string `json:"id"`     // 对应 RogueItem.ID
	OK     bool   `json:"ok"`     // 是否成功
	ErrMsg string `json:"errMsg"` // 失败原因
}

// BlacklistFileInfo 单个黑名单文件的更新结果
type BlacklistFileInfo struct {
	Name  string `json:"name"`  // 文件名
	Count int    `json:"count"` // 条目数
	OK    bool   `json:"ok"`    // 是否成功
	Err   string `json:"err"`   // 失败原因
}

// BlacklistUpdateResult 黑名单在线更新结果
type BlacklistUpdateResult struct {
	Time  string              `json:"time"`  // 更新时间
	Files []BlacklistFileInfo `json:"files"` // 各文件结果
}

// BlacklistInfo 当前黑名单状态
type BlacklistInfo struct {
	Dirs      int    `json:"dirs"`      // 目录名黑名单条数
	Paths     int    `json:"paths"`     // 相对路径黑名单条数
	Signs     int    `json:"signs"`     // 签名黑名单条数
	White     int    `json:"white"`     // 白名单条数
	LocalTime string `json:"localTime"` // 本地黑名单更新时间（空=使用内置）
	LocalDir  string `json:"localDir"`  // 本地黑名单目录
}
