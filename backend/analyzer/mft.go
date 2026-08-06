package analyzer

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// mftRecord 解析后的 MFT 记录（仅提取建树所需字段）
type mftRecord struct {
	num       uint32   // 记录号
	isDir     bool
	parent    uint32   // 父目录记录号（FILE_NAME 属性）
	name      string   // 文件名（优先非 DOS 命名空间）
	size      int64    // $DATA 大小（文件）
	isReparse bool     // 重解析点（联接/符号链接）
	dataExt   []uint32 // $DATA 属性所在的扩展记录号（$ATTRIBUTE_LIST）
}

// mftParser NTFS MFT 解析器
type mftParser struct {
	vol      windows.Handle
	recSize  int
	mftStart int64 // MFT 起始字节偏移
	mftBytes int64 // MFT 有效数据长度（字节）
}

const (
	attrTypeStandardInfo = 0x10
	attrTypeFileName     = 0x30
	attrTypeData         = 0x80
	attrTypeAttrList     = 0x20
	attrEnd              = 0xFFFFFFFF
)

// openVolume 以只读方式打开卷根（需要管理员权限）
func openVolume(drive string) (windows.Handle, error) {
	volumePath := fmt.Sprintf(`\\.\%s:`, strings.TrimSuffix(drive, ":"))
	h, err := windows.CreateFile(
		windows.StringToUTF16Ptr(volumePath),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("打开卷 %s 失败（需要管理员权限）: %w", volumePath, err)
	}
	return h, nil
}

// newMFTParser 打开卷并初始化 MFT 解析器
// 通过 FSCTL_GET_NTFS_VOLUME_DATA 官方接口获取 MFT 起始簇号与记录大小
func newMFTParser(drive string) (*mftParser, error) {
	h, err := openVolume(drive)
	if err != nil {
		return nil, err
	}
	close := func() { windows.CloseHandle(h) }

	// NTFS_VOLUME_DATA_BUFFER 完整结构约 112 字节，使用 256 字节缓冲并手工取字段
	buf := make([]byte, 256)
	var ret uint32
	err = windows.DeviceIoControl(
		h, windows.FSCTL_GET_NTFS_VOLUME_DATA,
		nil, 0,
		&buf[0], uint32(len(buf)), &ret, nil,
	)
	if err != nil {
		close()
		return nil, fmt.Errorf("获取 NTFS 卷信息失败（仅支持 NTFS 卷）: %w", err)
	}
	bytesPerCluster := binary.LittleEndian.Uint32(buf[0x2C:0x30])
	bytesPerRecord := binary.LittleEndian.Uint32(buf[0x30:0x34])
	mftValidLen := binary.LittleEndian.Uint64(buf[0x38:0x40])
	mftStartLcn := binary.LittleEndian.Uint64(buf[0x40:0x48])

	if bytesPerCluster == 0 || mftStartLcn == 0 {
		close()
		return nil, fmt.Errorf("NTFS 卷信息异常")
	}
	p := &mftParser{
		vol:      h,
		recSize:  int(bytesPerRecord),
		mftStart: int64(mftStartLcn) * int64(bytesPerCluster),
		mftBytes: int64(mftValidLen),
	}
	if p.recSize <= 0 {
		p.recSize = 1024
	}
	// 部分 Windows 11 卷 MFT 记录段为 4096（1 簇），自动检测修正
	p.recSize = detectRecSize(p)
	return p, nil
}

// detectRecSize 通过物理位置验证 MFT 记录大小：
// 读 mftStart + num*probe 处的记录，若其记录号字段 == num，则该 probe 为真实记录大小
func detectRecSize(p *mftParser) int {
	for _, probe := range []int{4096, 1024} {
		rec := make([]byte, probe)
		if err := p.readAt(rec, p.mftStart+int64(probe)*100); err == nil && len(rec) >= 0x30 {
			if string(rec[0:4]) == "FILE" {
				num := binary.LittleEndian.Uint32(rec[0x2C:0x30])
				if num == 100 {
					return probe
				}
			}
		}
	}
	return p.recSize
}

// readAt 从卷的绝对偏移读取数据（偏移需扇区对齐）
func (p *mftParser) readAt(buf []byte, offset int64) error {
	if _, err := windows.Seek(p.vol, offset, 0); err != nil {
		return err
	}
	var read uint32
	return windows.ReadFile(p.vol, buf, &read, nil)
}

// readRecord 读取并修复指定记录号的 MFT 记录
func (p *mftParser) readRecord(num uint32, buf []byte) error {
	off := p.mftStart + int64(num)*int64(p.recSize)
	if err := p.readAt(buf, off); err != nil {
		return err
	}
	applyFixup(buf)
	return nil
}

// applyFixup 应用更新序列数组（USN）修复：
// 每条 MFT 记录按 512 字节扇区存储，每个扇区末尾 2 字节被替换为 USN，
// 原始数据保存在记录内 uso 偏移处的数组中。修复 = 把数组项拷回扇区末尾。
func applyFixup(rec []byte) {
	if len(rec) < 10 {
		return
	}
	uso := int(binary.LittleEndian.Uint16(rec[4:6]))
	count := int(binary.LittleEndian.Uint16(rec[6:8]))
	if uso < 8 || count < 2 || uso+count*2 > len(rec) {
		return
	}
	arr := rec[uso : uso+count*2]
	for i := 1; i < count; i++ {
		dst := i*512 - 2 // 第 i 个扇区（从 0 起）的末尾 2 字节
		if dst+2 <= len(rec) {
			copy(rec[dst:dst+2], arr[i*2:i*2+2])
		}
	}
}

// parseMFTRecord 解析单条 MFT 记录
func parseMFTRecord(rec []byte) *mftRecord {
	if len(rec) < 0x40 || string(rec[0:4]) != "FILE" {
		return nil
	}
	flags := binary.LittleEndian.Uint16(rec[0x16:0x18])
	if flags&0x01 == 0 {
		return nil // 记录未使用
	}
	r := &mftRecord{
		num:   binary.LittleEndian.Uint32(rec[0x2C:0x30]),
		isDir: flags&0x02 != 0,
	}
	attrOff := int(binary.LittleEndian.Uint16(rec[0x14:0x16]))
	if attrOff < 0x18 || attrOff > len(rec) {
		return nil
	}
	for off := attrOff; off+8 <= len(rec); {
		atype := binary.LittleEndian.Uint32(rec[off : off+4])
		if atype == attrEnd {
			break
		}
		alen := int(binary.LittleEndian.Uint32(rec[off+4 : off+8]))
		if alen < 8 || off+alen > len(rec) {
			break
		}
		nonRes := rec[off+8]

		switch atype {
		case attrTypeFileName:
			if nonRes == 0 && off+0x18+4 <= len(rec) {
				valLen := int(binary.LittleEndian.Uint32(rec[off+0x10 : off+0x14]))
				valOff := int(binary.LittleEndian.Uint16(rec[off+0x14 : off+0x16]))
				if valOff+valLen <= alen && valLen >= 0x42 {
					parseFileName(rec[off+valOff:off+valOff+valLen], r)
				}
			}
		case attrTypeData:
			// 仅取首个未命名数据流大小（命名流/扩展属性不重复计）
			if r.size == 0 {
				if nonRes == 1 {
					if off+0x38 <= len(rec) {
						r.size = int64(binary.LittleEndian.Uint64(rec[off+0x30 : off+0x38]))
					}
				} else if off+0x18 <= len(rec) {
					r.size = int64(binary.LittleEndian.Uint32(rec[off+0x10 : off+0x14]))
				}
			}
		case attrTypeAttrList:
			// 属性列表：解析 $DATA 的扩展记录号，用于后续补大小
			if nonRes == 0 && off+0x18 <= len(rec) {
				valLen := int(binary.LittleEndian.Uint32(rec[off+0x10 : off+0x14]))
				valOff := int(binary.LittleEndian.Uint16(rec[off+0x14 : off+0x16]))
				if valOff+valLen <= alen {
					parseAttrList(rec[off+valOff:off+valOff+valLen], r)
				}
			}
		}
		off += alen
	}
	return r
}

// parseAttrList 解析 $ATTRIBUTE_LIST 内容，收集 $DATA 属性的扩展记录号
// 条目布局：0x00 type(4) 0x04 len(2) 0x06 nameLen(1) 0x07 nameOff(1)
//          0x08 lowestVCN(8) 0x10 fileRef(8) 0x18 name...
func parseAttrList(data []byte, r *mftRecord) {
	for off := 0; off+0x18 <= len(data); {
		atype := binary.LittleEndian.Uint32(data[off : off+4])
		recLen := int(binary.LittleEndian.Uint16(data[off+4 : off+6]))
		if recLen < 0x18 || off+recLen > len(data) {
			break
		}
		if atype == attrTypeData {
			extRef := binary.LittleEndian.Uint64(data[off+0x10:off+0x18]) & 0xFFFFFFFFFFFF
			if extRef > 0 && uint32(extRef) != r.num {
				r.dataExt = append(r.dataExt, uint32(extRef))
			}
		}
		off += recLen
	}
}

// parseFileName 解析 FILE_NAME 属性值（取父目录、名称、重解析标记）
// 布局：0x00 parent(8) 0x08-0x2F 时间戳 0x30 allocated(8) 0x38 real(8)
//      0x40 nameLen(1) 0x41 namespace(1) 0x42 name(UTF-16LE)
func parseFileName(data []byte, r *mftRecord) {
	parent := binary.LittleEndian.Uint64(data[0:8]) & 0xFFFFFFFFFFFF
	if len(data) >= 0x40 {
		reparse := binary.LittleEndian.Uint32(data[0x3C:0x40])
		if reparse != 0 {
			r.isReparse = true
		}
	}
	nameLen := int(data[0x40])
	ns := data[0x41]
	if nameLen <= 0 || 0x42+nameLen*2 > len(data) {
		return
	}
	name := utf16BytesToString(data[0x42 : 0x42+nameLen*2])
	if name == "" || name == "." {
		return
	}
	// 优先保留非 DOS 命名空间（namespace 0=POSIX,1=Win32,3=Win32+DOS）
	if ns == 2 && r.name != "" {
		return
	}
	if ns != 2 {
		r.name = name
	} else if r.name == "" {
		r.name = name
	}
	r.parent = uint32(parent)
}

// utf16BytesToString UTF-16LE 字节转字符串
func utf16BytesToString(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return string(utf16.Decode(u))
}
