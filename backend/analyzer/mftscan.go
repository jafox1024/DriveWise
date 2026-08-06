package analyzer

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"drivewise/backend/models"
)

// 全量 MFT 树缓存：drive -> 完整文件树（不截断），供下钻查询
var (
	mftCache   = map[string]*models.FileNode{}
	mftCacheMu sync.RWMutex
)

// scanMFTVolume 解析卷的 MFT，构建全量文件树
func scanMFTVolume(drive string) (*models.FileNode, error) {
	p, err := newMFTParser(drive)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(p.vol)

	mftTotal := p.mftBytes // MFT 有效数据长度（来自 FSCTL_GET_NTFS_VOLUME_DATA）

	// 顺序分块读取全部 MFT 记录
	const blockRecords = 4096
	block := make([]byte, p.recSize*blockRecords)
	records := make([]*mftRecord, 0, 1<<18)

	off := p.mftStart
	for {
		// 已读取字节 = off - p.mftStart
		readLen := len(block)
		if mftTotal > 0 && off-p.mftStart >= mftTotal {
			break
		}
		if mftTotal > 0 && off-p.mftStart+int64(readLen) > mftTotal {
			readLen = int(mftTotal - (off - p.mftStart))
			if readLen <= 0 {
				break
			}
			block = block[:readLen]
		}
		n, err := p.readBlock(block, off)
		if err != nil || n <= 0 {
			break
		}
		for i := 0; i+p.recSize <= n; i += p.recSize {
			rec := block[i : i+p.recSize]
			if len(rec) >= 4 && string(rec[0:4]) == "FILE" {
				applyFixup(rec)
				if r := parseMFTRecord(rec); r != nil && (r.name != "" || r.num == 5) {
					records = append(records, r)
				}
			}
		}
		off += int64(n)
	}

	// 第二遍：$ATTRIBUTE_LIST 扩展记录补全文件大小
	extBuf := make([]byte, p.recSize)
	for _, r := range records {
		if r.size == 0 && len(r.dataExt) > 0 {
			if err := p.readRecord(r.dataExt[0], extBuf); err == nil {
				if er := parseMFTRecord(extBuf); er != nil && er.size > 0 {
					r.size = er.size
				}
			}
		}
	}

	// $BadClus 映射整个卷（大小=卷容量），虚高 300GB+，排除
	for _, r := range records {
		if r.name == "$BadClus" || r.num == 7 {
			r.size = 0
		}
	}

	return buildMFTPathTree(records, drive), nil
}

// readBlock 从卷偏移读取一整块 MFT 数据（卷读取可能部分返回，循环读满）
func (p *mftParser) readBlock(buf []byte, offset int64) (int, error) {
	if _, err := windows.Seek(p.vol, offset, 0); err != nil {
		return 0, err
	}
	total := 0
	for total < len(buf) {
		var read uint32
		if err := windows.ReadFile(p.vol, buf[total:], &read, nil); err != nil {
			if total > 0 {
				break
			}
			return 0, err
		}
		if read == 0 {
			break
		}
		total += int(read)
	}
	return total, nil
}

// buildMFTPathTree 按父记录号重建目录树并聚合大小
func buildMFTPathTree(records []*mftRecord, drive string) *models.FileNode {
	const rootNum = 5 // NTFS 根目录固定记录号 5

	// 记录号 -> 节点
	byNum := make(map[uint32]*models.FileNode, len(records)+16)
	for _, r := range records {
		node := &models.FileNode{Name: r.name, IsDir: r.isDir, Size: r.size}
		byNum[r.num] = node
	}

	// 建立父子关系（孤儿记录跳过，避免污染根目录树）
	root := byNum[rootNum]
	if root == nil {
		root = &models.FileNode{Name: drive + "\\", IsDir: true}
	}
	root.Name = drive + "\\"
	root.Path = drive + ":\\"
	for _, r := range records {
		node := byNum[r.num]
		if node == nil || r.num == rootNum {
			continue
		}
		parent := byNum[r.parent]
		if parent == nil || parent == node {
			continue // parent 缺失/自引用：跳过（$ATTRIBUTE_LIST 扩展记录或已删除项）
		}
		parent.Children = append(parent.Children, node)
	}

	// 后序聚合目录大小（显式栈防深递归）
	// 第一遍：先序遍历入栈
	order := make([]*models.FileNode, 0, len(byNum))
	stack := []*models.FileNode{root}
	visited := make(map[*models.FileNode]bool, len(byNum))
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[n] {
			continue
		}
		visited[n] = true
		order = append(order, n)
		// 避免误挂成环：防御
		for _, c := range n.Children {
			if !visited[c] {
				stack = append(stack, c)
			}
		}
	}
	// 第二遍：逆序聚合（子先于父）
	for i := len(order) - 1; i >= 0; i-- {
		n := order[i]
		for _, c := range n.Children {
			n.Size += c.Size
		}
	}

	// 补全路径
	fillPaths(root, drive+":\\")

	// 去重/去空 children（防御性）
	cleanChildren(root)
	return root
}

// fillPaths 深度优先补全节点路径
func fillPaths(n *models.FileNode, base string) {
	sep := "\\"
	if strings.HasSuffix(base, "\\") {
		sep = ""
	}
	for _, c := range n.Children {
		c.Path = base + sep + c.Name
		fillPaths(c, c.Path)
	}
}

// cleanChildren 清理空 children 并去重
func cleanChildren(n *models.FileNode) {
	seen := make(map[string]bool, len(n.Children))
	kept := n.Children[:0]
	for _, c := range n.Children {
		if c == nil || seen[c.Path] {
			continue
		}
		seen[c.Path] = true
		kept = append(kept, c)
		cleanChildren(c)
	}
	n.Children = kept
}

// truncateTree 按每层 TopN 截断树，其余聚合为“其他”
func truncateTree(root *models.FileNode, topN int) *models.FileNode {
	if topN <= 0 {
		topN = 50
	}
	out := &models.FileNode{Name: root.Name, Path: root.Path, Size: root.Size, IsDir: true}
	for _, c := range root.Children {
		out.Children = append(out.Children, truncateChildren(c, topN))
	}
	return out
}

func truncateChildren(n *models.FileNode, topN int) *models.FileNode {
	cp := &models.FileNode{Name: n.Name, Path: n.Path, Size: n.Size, IsDir: n.IsDir}
	if !n.IsDir || len(n.Children) == 0 {
		return cp
	}
	sorted := append([]*models.FileNode(nil), n.Children...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Size > sorted[j].Size })
	if len(sorted) > topN {
		var rest int64
		for _, c := range sorted[topN:] {
			rest += c.Size
		}
		sorted = append(sorted[:topN:topN], &models.FileNode{
			Name: "其他", Path: n.Path + "\\其他", Size: rest, IsDir: true,
		})
	}
	for _, c := range sorted {
		cp.Children = append(cp.Children, truncateChildren(c, topN))
	}
	return cp
}

// ScanMFT 以 MFT 方式扫描磁盘，返回截断树并缓存全量树
func (s *AnalyzerService) ScanMFT(drive string, topN int) (*models.FileNode, error) {
	drive = strings.TrimSuffix(drive, ":")
	full, err := scanMFTVolume(drive)
	if err != nil {
		return nil, err
	}
	mftCacheMu.Lock()
	mftCache[strings.ToUpper(drive)] = full
	mftCacheMu.Unlock()
	return truncateTree(full, topN), nil
}

// mftLookup 从缓存中查找路径对应的直接子级（返回全量子级）
func mftLookup(path string) []*models.FileNode {
	path = filepath.Clean(path)
	mftCacheMu.RLock()
	defer mftCacheMu.RUnlock()
	for _, root := range mftCache {
		target, ok := findNodeByPath(root, path)
		if ok && target.IsDir {
			return target.Children
		}
	}
	return nil
}

// mftInvalidatePath 从 MFT 缓存树中移除指定路径节点，并沿祖先链扣减大小。
// 在删除/移入回收站后调用，保证磁盘分析页删除后立即刷新、不残留幽灵节点。
func mftInvalidatePath(path string) {
	path = filepath.Clean(path)
	norm := normalizePath(path)

	mftCacheMu.Lock()
	defer mftCacheMu.Unlock()

	for _, root := range mftCache {
		// BFS 建立 子->父 映射（单次遍历）
		parents := make(map[*models.FileNode]*models.FileNode)
		queue := []*models.FileNode{root}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			for _, c := range n.Children {
				if c == nil {
					continue
				}
				parents[c] = n
				queue = append(queue, c)
			}
		}

		// 定位目标节点与其父节点
		var target, parent *models.FileNode
		for _, c := range root.Children {
			if normalizePath(c.Path) == norm {
				target, parent = c, root
				break
			}
		}
		if target == nil {
			for c, p := range parents {
				if normalizePath(c.Path) == norm {
					target, parent = c, p
					break
				}
			}
		}
		if target == nil {
			continue
		}

		removed := target.Size
		kept := parent.Children[:0]
		for _, c := range parent.Children {
			if c != target {
				kept = append(kept, c)
			}
		}
		parent.Children = kept

		// 祖先链大小递减（含父节点）
		for p := parent; p != nil; p = parents[p] {
			p.Size -= removed
			if p.Size < 0 {
				p.Size = 0
			}
		}
	}
}

// findNodeByPath 在树中按路径查找节点（BFS，避免深递归）
func findNodeByPath(root *models.FileNode, path string) (*models.FileNode, bool) {
	norm := normalizePath(path)
	queue := []*models.FileNode{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if normalizePath(n.Path) == norm {
			return n, true
		}
		queue = append(queue, n.Children...)
	}
	return nil, false
}

func normalizePath(p string) string {
	return strings.ToLower(strings.TrimRight(filepath.Clean(p), `\/`))
}

// GetDirChildren 获取目录直接子级：优先 MFT 缓存，否则目录遍历（单层）
func (s *AnalyzerService) GetDirChildren(path string) []*models.FileNode {
	if kids := mftLookup(path); kids != nil {
		return kids
	}
	node, err := scanTree(path, 0, 1, 200)
	if err != nil || node == nil {
		return nil
	}
	return node.Children
}
