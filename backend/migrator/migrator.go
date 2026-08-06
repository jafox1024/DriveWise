package migrator

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"drivewise/backend/internal/fsutil"
	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
)

// MigratorService 软件迁移服务，作为 Wails v3 服务暴露给前端
type MigratorService struct{}

// MigrateRecord 迁移记录
type MigrateRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`       // 应用名
	SrcPath    string `json:"srcPath"`    // 原始路径（现为联接点）
	TargetPath string `json:"targetPath"` // 数据实际位置
	Time       string `json:"time"`
}

const copyConcurrency = 8

// scanRoots 候选迁移根目录
var scanRoots = []string{
	`%LOCALAPPDATA%`,
	`%APPDATA%`,
}

// skipPrefixes 跳过系统/不可迁移目录
var skipPrefixes = []string{
	"Microsoft", "Packages", "Google", "Temp", "TempState",
	"Application Data", "History", "Adobe", "Apple", "MicrosoftEdge",
}

// ScanApps 扫描可迁移的应用目录，按大小降序返回
func (s *MigratorService) ScanApps() []models.MigratableApp {
	seen := make(map[string]bool)
	var apps []models.MigratableApp

	for _, rootTmpl := range scanRoots {
		root := winutil.ExpandEnv(rootTmpl)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if skipName(name) {
				continue
			}
			full := filepath.Join(root, name)
			if seen[full] {
				continue
			}
			seen[full] = true
			size, count := fsutil.DirSize(full, 0)
			// 仅列出大于 100MB 的目录
			if size < 100*1024*1024 {
				continue
			}
			apps = append(apps, models.MigratableApp{
				Name:      name,
				Path:      full,
				Size:      size,
				FileCount: count,
			})
		}
	}

	sort.Slice(apps, func(i, j int) bool { return apps[i].Size > apps[j].Size })
	return apps
}

func skipName(name string) bool {
	n := strings.ToLower(name)
	for _, p := range skipPrefixes {
		if strings.HasPrefix(n, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// MoveApp 迁移应用：复制到目标盘，然后将原目录替换为目录联接
func (s *MigratorService) MoveApp(srcPath, dstPath string) error {
	srcAbs, err := filepath.Abs(srcPath)
	if err != nil {
		return err
	}
	dstAbs, err := filepath.Abs(dstPath)
	if err != nil {
		return err
	}

	info, err := os.Lstat(srcAbs)
	if err != nil {
		return fmt.Errorf("源路径不存在: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("源路径不是目录: %s", srcAbs)
	}
	if filepath.VolumeName(srcAbs) == filepath.VolumeName(dstAbs) {
		return fmt.Errorf("目标与源位于同一磁盘，无法释放空间")
	}
	if _, err := os.Lstat(dstAbs); err == nil {
		return fmt.Errorf("目标路径已存在: %s", dstAbs)
	}

	// 1. 复制到目标
	if err := copyDir(srcAbs, dstAbs); err != nil {
		// 清理失败残留
		_ = os.RemoveAll(dstAbs)
		return fmt.Errorf("复制失败: %w", err)
	}

	// 2. 验证复制完整性（比较顶层条目数）
	if err := verifyCopy(srcAbs, dstAbs); err != nil {
		_ = os.RemoveAll(dstAbs)
		return fmt.Errorf("复制校验失败: %w", err)
	}

	// 3. 原目录改名（临时占位）
	backup := srcAbs + ".drivewise.bak"
	if err := os.Rename(srcAbs, backup); err != nil {
		_ = os.RemoveAll(dstAbs)
		return fmt.Errorf("原目录重命名失败（可能有程序占用），已回滚: %w", err)
	}

	// 4. 创建目录联接：src -> dst
	if err := createJunction(srcAbs, dstAbs); err != nil {
		// 回滚：恢复原目录
		_ = os.RemoveAll(dstAbs)
		_ = os.Rename(backup, srcAbs)
		return fmt.Errorf("创建目录联接失败，已回滚: %w", err)
	}

	// 5. 删除备份目录
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("迁移成功，但清理临时备份失败（%s），可手动删除", backup)
	}

	// 6. 写入迁移记录
	records, _ := loadRecords()
	records = append(records, MigrateRecord{
		ID:         fmt.Sprintf("%x", fnvHash(srcAbs)),
		Name:       filepath.Base(srcAbs),
		SrcPath:    srcAbs,
		TargetPath: dstAbs,
		Time:       time.Now().Format("2006-01-02 15:04:05"),
	})
	_ = saveRecords(records)
	return nil
}

// RestoreApp 恢复迁移：删除联接，将目标内容移回原路径
func (s *MigratorService) RestoreApp(junctionPath string) error {
	linkAbs, err := filepath.Abs(junctionPath)
	if err != nil {
		return err
	}
	// 校验是联接
	info, err := os.Lstat(linkAbs)
	if err != nil {
		return fmt.Errorf("路径不存在: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s 不是目录联接", linkAbs)
	}
	target, err := os.Readlink(linkAbs)
	if err != nil {
		return fmt.Errorf("读取联接目标失败: %w", err)
	}
	// 删除联接（仅删除联接本身）
	if err := os.Remove(linkAbs); err != nil {
		return fmt.Errorf("删除联接失败: %w", err)
	}
	// 移动内容回来
	if err := os.Rename(target, linkAbs); err != nil {
		return fmt.Errorf("数据移动失败，联接已删除但数据仍在 %s: %w", target, err)
	}
	// 移除迁移记录
	records, _ := loadRecords()
	for i := range records {
		if records[i].SrcPath == linkAbs {
			records = append(records[:i], records[i+1:]...)
			break
		}
	}
	_ = saveRecords(records)
	return nil
}

// GetMigrations 列出所有已迁移的应用（用于恢复）
func (s *MigratorService) GetMigrations() []MigrateRecord {
	records, _ := loadRecords()
	return records
}

// ============ 记录持久化 ============

func recordsPath() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "DriveWise", "migrations.json")
}

func loadRecords() ([]MigrateRecord, error) {
	data, err := os.ReadFile(recordsPath())
	if err != nil {
		return nil, err
	}
	var recs []MigrateRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, err
	}
	return recs, nil
}

func saveRecords(recs []MigrateRecord) error {
	if err := os.MkdirAll(filepath.Dir(recordsPath()), 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(recs, "", "  ")
	return os.WriteFile(recordsPath(), data, 0o644)
}

func fnvHash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// createJunction 使用 Windows 内置 mklink /J 创建目录联接（无需管理员权限）
// 注意：mklink 是 cmd 内置命令，需经 cmd /c 调用；参数顺序为 mklink /J <联接> <目标>
func createJunction(link, target string) error {
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s（%s）", strings.TrimSpace(string(out)), err.Error())
	}
	return nil
}

// copyDir 并发复制目录树（跳过联接）
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	sem := make(chan struct{}, copyConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, e := range entries {
		e := e
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())

		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			var err error
			if e.IsDir() {
				err = copyDir(srcPath, dstPath)
			} else {
				err = copyFile(srcPath, dstPath)
			}
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if info, ierr := in.Stat(); ierr == nil {
		_ = os.Chmod(dst, info.Mode())
	}
	return nil
}

// verifyCopy 对比源与目标顶层条目数量，粗略校验复制完整性
func verifyCopy(src, dst string) error {
	srcEntries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	dstEntries, err := os.ReadDir(dst)
	if err != nil {
		return err
	}
	if len(srcEntries) != len(dstEntries) {
		return fmt.Errorf("条目数不一致：源 %d，目标 %d", len(srcEntries), len(dstEntries))
	}
	return nil
}
