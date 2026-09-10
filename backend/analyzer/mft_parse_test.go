package analyzer

import (
	"encoding/binary"
	"testing"
)

// residentAttr 构造驻留属性：type + 可选名字（UTF-8，转 UTF-16 存储）+ 值
func residentAttr(typ uint32, name []byte, value []byte) []byte {
	nameLen := len(name)
	hdr := 0x18
	total := hdr + nameLen*2 + len(value)
	if total%8 != 0 {
		total += 8 - total%8
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint32(buf[0:4], typ)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(total))
	buf[8] = 0 // resident
	buf[9] = byte(nameLen)
	binary.LittleEndian.PutUint16(buf[0x0A:0x0C], uint16(hdr)) // name offset
	binary.LittleEndian.PutUint32(buf[0x10:0x14], uint32(len(value)))
	binary.LittleEndian.PutUint16(buf[0x14:0x16], uint16(hdr+nameLen*2)) // value offset
	for i, c := range name {
		binary.LittleEndian.PutUint16(buf[hdr+i*2:hdr+i*2+2], uint16(c))
	}
	copy(buf[hdr+nameLen*2:], value)
	return buf
}

// nonResidentDataAttr 构造非驻留未命名 $DATA 属性（仅填充 real size）
func nonResidentDataAttr(realSize uint64) []byte {
	buf := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(buf[0:4], attrTypeData)
	binary.LittleEndian.PutUint32(buf[4:8], 0x40)
	buf[8] = 1 // non-resident
	buf[9] = 0 // nameLen = 0（未命名流）
	binary.LittleEndian.PutUint64(buf[0x30:0x38], realSize)
	return buf
}

// buildFileRecord 构造带 FILE_NAME + 命名流 + 未命名流的 MFT 文件记录
func buildFileRecord(t *testing.T, num uint32) []byte {
	t.Helper()
	rec := make([]byte, 1024)
	copy(rec[0:4], "FILE")
	binary.LittleEndian.PutUint16(rec[0x14:0x16], 0x38) // attributes offset
	binary.LittleEndian.PutUint16(rec[0x16:0x18], 0x0001)
	binary.LittleEndian.PutUint32(rec[0x2C:0x30], num)

	fnValue := make([]byte, 0x4C)
	binary.LittleEndian.PutUint64(fnValue[0:8], 5) // parent = 5（根目录）
	fnValue[0x40] = 4
	fnValue[0x41] = 1
	copy(fnValue[0x42:], []byte{'t', 0, 'e', 0, 's', 0, 't', 0})
	fn := residentAttr(attrTypeFileName, nil, fnValue)

	// 命名数据流先出现（如 Zone.Identifier）
	ads := residentAttr(attrTypeData, []byte("Zone.Identifier"), []byte("[ZoneTransfer]\r\nZoneId=3\r\n"))

	// 未命名数据流后出现
	data := nonResidentDataAttr(1000)

	off := 0x38
	copy(rec[off:], fn)
	off += len(fn)
	copy(rec[off:], ads)
	off += len(ads)
	copy(rec[off:], data)
	off += len(data)
	binary.LittleEndian.PutUint32(rec[off:off+4], 0xFFFFFFFF)
	return rec
}

// TestParseMFTRecordIgnoresNamedStream 命名数据流（ADS）不得覆盖未命名流大小
func TestParseMFTRecordIgnoresNamedStream(t *testing.T) {
	rec := buildFileRecord(t, 1)
	r := parseMFTRecord(rec)
	if r == nil {
		t.Fatal("记录解析失败")
	}
	if r.name != "test" {
		t.Fatalf("名称应为 test，实际 %q", r.name)
	}
	if r.parent != 5 {
		t.Fatalf("父目录应为 5，实际 %d", r.parent)
	}
	if r.size != 1000 {
		t.Fatalf("大小应为未命名流 1000，实际 %d（命名流被误统计）", r.size)
	}
}

// TestParseAttrListOnlyUnnamedData 属性列表只收集未命名 $DATA，并收集 FILE_NAME
func TestParseAttrListOnlyUnnamedData(t *testing.T) {
	data := make([]byte, 3*0x18)
	// 条目 1：未命名 $DATA，扩展记录 100
	binary.LittleEndian.PutUint32(data[0:4], attrTypeData)
	binary.LittleEndian.PutUint16(data[4:6], 0x18)
	data[6] = 0 // nameLen = 0
	binary.LittleEndian.PutUint64(data[0x10:0x18], 100)
	// 条目 2：命名 $DATA，扩展记录 200（不应收集）
	binary.LittleEndian.PutUint32(data[0x18:0x1C], attrTypeData)
	binary.LittleEndian.PutUint16(data[0x1C:0x1E], 0x18)
	data[0x1E] = 1 // nameLen = 1
	binary.LittleEndian.PutUint64(data[0x28:0x30], 200)
	// 条目 3：FILE_NAME，扩展记录 300（应收集到 nameExt）
	binary.LittleEndian.PutUint32(data[0x30:0x34], attrTypeFileName)
	binary.LittleEndian.PutUint16(data[0x34:0x36], 0x18)
	data[0x36] = 0
	binary.LittleEndian.PutUint64(data[0x40:0x48], 300)

	r := &mftRecord{num: 1}
	parseAttrList(data, r)

	if len(r.dataExt) != 1 || r.dataExt[0] != 100 {
		t.Fatalf("dataExt 应只含未命名流 100，实际 %v", r.dataExt)
	}
	if len(r.nameExt) != 1 || r.nameExt[0] != 300 {
		t.Fatalf("nameExt 应含 FILE_NAME 300，实际 %v", r.nameExt)
	}
}

// TestParseMFTRecordSelfReferenceInAttrList 属性列表引用自身记录号时忽略，避免环
func TestParseMFTRecordSelfReferenceInAttrList(t *testing.T) {
	data := make([]byte, 0x18)
	binary.LittleEndian.PutUint32(data[0:4], attrTypeData)
	binary.LittleEndian.PutUint16(data[4:6], 0x18)
	binary.LittleEndian.PutUint64(data[0x10:0x18], 7) // 自身记录号 7

	r := &mftRecord{num: 7}
	parseAttrList(data, r)
	if len(r.dataExt) != 0 {
		t.Fatalf("自身引用不应收集，实际 %v", r.dataExt)
	}
}
