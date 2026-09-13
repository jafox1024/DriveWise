package cleaner

import (
	"testing"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
)

// TestRegKeyTreeBackupDeleteRestore 验证「整棵子树备份 → 递归删除 → 原样还原」闭环。
// 重点覆盖 RegDeleteKey 删不掉的「非空键」（驱动器右键静态动词带 Command 子键的场景）。
func TestRegKeyTreeBackupDeleteRestore(t *testing.T) {
	const root = `Software\DriveWiseTest\SubTreeProbe`
	hive := registry.CURRENT_USER
	defer func() { _ = deleteKeyTree(hive, root) }() // 保证测试后干净

	// 构造：默认值 + DWORD 值 + 子键 Command（默认值 + 子键 Extra 深层）
	k, _, err := registry.CreateKey(hive, root, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("创建测试键失败: %v", err)
	}
	if err := k.SetStringValue("", "DriveWise 测试动词"); err != nil {
		t.Fatalf("写默认值失败: %v", err)
	}
	if err := k.SetDWordValue("Flag", 0xABCD1234); err != nil {
		t.Fatalf("写 DWORD 失败: %v", err)
	}
	k.Close()

	ck, _, err := registry.CreateKey(hive, root+`\Command`, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("创建 Command 子键失败: %v", err)
	}
	if err := ck.SetStringValue("", `"C:\probe\verb.exe" %1`); err != nil {
		t.Fatalf("写 Command 默认值失败: %v", err)
	}
	ck.Close()

	ek, _, err := registry.CreateKey(hive, root+`\Command\Extra`, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("创建深层子键失败: %v", err)
	}
	if err := ek.SetExpandStringValue("Path", `%SystemRoot%\probe`); err != nil {
		t.Fatalf("写 EXPAND_SZ 失败: %v", err)
	}
	ek.Close()

	// 1) 备份整棵子树
	tree, err := dumpKeyTree(hive, root)
	if err != nil {
		t.Fatalf("dumpKeyTree 失败: %v", err)
	}
	if tree == "" {
		t.Fatal("备份为空")
	}

	// 2) 递归删除（旧实现此处会因非空键报 Access is denied）
	if err := deleteKeyTree(hive, root); err != nil {
		t.Fatalf("deleteKeyTree 失败: %v", err)
	}
	if _, oerr := registry.OpenKey(hive, root, registry.QUERY_VALUE); oerr == nil {
		t.Fatal("删除后键仍存在")
	}

	// 3) 按备份还原
	if err := restoreKeyTree(hive, root, tree); err != nil {
		t.Fatalf("restoreKeyTree 失败: %v", err)
	}
	rk, err := registry.OpenKey(hive, root, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("还原后键不存在: %v", err)
	}
	def, _, _ := rk.GetStringValue("")
	flag, _, _ := rk.GetIntegerValue("Flag")
	rk.Close()
	if def != "DriveWise 测试动词" {
		t.Fatalf("默认值还原错误: %q", def)
	}
	if flag != 0xABCD1234 {
		t.Fatalf("DWORD 还原错误: %#x", flag)
	}
	rc, err := registry.OpenKey(hive, root+`\Command`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("Command 子键未还原: %v", err)
	}
	cdef, _, _ := rc.GetStringValue("")
	rc.Close()
	if cdef != `"C:\probe\verb.exe" %1` {
		t.Fatalf("Command 默认值还原错误: %q", cdef)
	}
	re, err := registry.OpenKey(hive, root+`\Command\Extra`, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("深层子键未还原: %v", err)
	}
	pv, valtype, _ := re.GetStringValue("Path")
	re.Close()
	if pv != `%SystemRoot%\probe` {
		t.Fatalf("EXPAND_SZ 还原错误: %q", pv)
	}
	if valtype != registry.EXPAND_SZ {
		t.Fatalf("EXPAND_SZ 类型丢失: %d", valtype)
	}
}

// TestBackupAndRemoveKeyNonEmpty 验证带子键的驱动器右键动词能真正被清理（回归 R-命名空间清理失败）。
func TestBackupAndRemoveKeyNonEmpty(t *testing.T) {
	const root = `Software\DriveWiseTest\VerbProbe`
	hive := registry.CURRENT_USER
	defer func() { _ = deleteKeyTree(hive, root) }()

	k, _, err := registry.CreateKey(hive, root, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("创建测试键失败: %v", err)
	}
	_ = k.SetStringValue("", "探针动词")
	k.Close()
	ck, _, err := registry.CreateKey(hive, root+`\Command`, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("创建 Command 子键失败: %v", err)
	}
	_ = ck.SetStringValue("", `"C:\probe.exe"`)
	ck.Close()

	item := &models.RogueItem{
		ID: "probe-verb", Name: "探针动词", RuleType: "namespace",
		Path: `HKCU\` + root, Value: "探针动词", Action: "remove", RiskLevel: "low",
	}
	entry := &BackupEntry{ID: item.ID, Name: item.Name, RuleType: item.RuleType}
	got, err := backupAndRemoveKey(item, entry)
	if err != nil {
		t.Fatalf("backupAndRemoveKey 失败（回归：非空键删不掉）: %v", err)
	}
	if got.RegTree == "" {
		t.Fatal("未记录整棵子树备份")
	}
	if _, oerr := registry.OpenKey(hive, root, registry.QUERY_VALUE); oerr == nil {
		t.Fatal("清理后键仍存在")
	}
	// 还原后用 restoreKeyTree 校验（restoreEntry 走 manifest，这里直接验证数据可用）
	if rerr := restoreKeyTree(hive, got.RegKey, got.RegTree); rerr != nil {
		t.Fatalf("按备份还原失败: %v", rerr)
	}
	if _, oerr := registry.OpenKey(hive, root+`\Command`, registry.QUERY_VALUE); oerr != nil {
		t.Fatalf("还原后 Command 子键缺失: %v", oerr)
	}
}
