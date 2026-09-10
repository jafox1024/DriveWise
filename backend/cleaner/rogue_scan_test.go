package cleaner

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/models"
)

// TestExtractExePath 解析启动命令中的 exe 路径
func TestExtractExePath(t *testing.T) {
	appData := os.Getenv("LOCALAPPDATA")
	exe := filepath.Join(appData, "someapp", "app.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err == nil {
		_ = os.WriteFile(exe, []byte("MZ"), 0o644)
		defer os.RemoveAll(filepath.Dir(exe))
	}

	cases := []struct {
		in   string
		want string
	}{
		{`"` + exe + `" /background`, exe},
		{exe + ` -arg`, exe},
		{`%LOCALAPPDATA%\someapp\app.exe --flag`, exe},
		{`C:\Windows\System32\cmd.exe /c start`, ""}, // 系统目录排除
		{`rundll32.exe url.dll,x`, ""},                // 相对路径不存在
		{`"C:\some\missing\path.exe"`, ""},            // 文件不存在
	}
	for _, c := range cases {
		if got := extractExePath(c.in); got != c.want {
			t.Errorf("extractExePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDecodeUTF16Bytes 验证注册表字符串原始字节解码
func TestDecodeUTF16Bytes(t *testing.T) {
	// "abc" 的 UTF-16LE（含尾 NUL）
	raw := []byte{'a', 0, 'b', 0, 'c', 0, 0, 0}
	if got := decodeUTF16Bytes(raw); got != "abc" {
		t.Fatalf("decodeUTF16Bytes = %q, want abc", got)
	}
	if got := decodeUTF16Bytes(nil); got != "" {
		t.Fatalf("decodeUTF16Bytes(nil) = %q, want empty", got)
	}
}

// TestRegistryBackupRestoreRoundtrip 验证注册表备份/恢复按类型原样还原（DWORD/BINARY）
func TestRegistryBackupRestoreRoundtrip(t *testing.T) {
	const testKey = `Software\DriveWiseTest\Roundtrip`
	registry.DeleteKey(registry.CURRENT_USER, `Software\DriveWiseTest`)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, testKey, registry.SET_VALUE)
	if err != nil {
		t.Skipf("无法创建测试键: %v", err)
	}
	key.SetDWordValue("dw", 123456)
	bin := []byte{1, 2, 3, 4, 5}
	key.SetBinaryValue("blob", bin)
	key.Close()
	defer registry.DeleteKey(registry.CURRENT_USER, `Software\DriveWiseTest`)

	// ---- DWORD 值清理 + 恢复 ----
	item := &models.RogueItem{Path: `HKCU\` + testKey + `\dw`}
	entry := &BackupEntry{}
	entry2, err := backupAndRemoveRegistry(item, entry)
	if err != nil {
		t.Fatalf("backupAndRemoveRegistry: %v", err)
	}
	if entry2.RegKind != registry.DWORD {
		t.Fatalf("RegKind = %d, want DWORD(%d)", entry2.RegKind, registry.DWORD)
	}
	if v := binary.LittleEndian.Uint32(entry2.RegBlob); v != 123456 {
		t.Fatalf("RegBlob = %d, want 123456", v)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, testKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := restoreRegistryValue(k, entry2.RegValue, entry2); err != nil {
		t.Fatalf("restore: %v", err)
	}
	k.Close()
	k, _ = registry.OpenKey(registry.CURRENT_USER, testKey, registry.QUERY_VALUE)
	got, _, err := k.GetIntegerValue("dw")
	k.Close()
	if err != nil || got != 123456 {
		t.Fatalf("DWORD 恢复失败: got=%d err=%v", got, err)
	}

	// ---- BINARY 值清理 + 恢复 ----
	itemB := &models.RogueItem{Path: `HKCU\` + testKey + `\blob`}
	entryB := &BackupEntry{}
	entryB2, err := backupAndRemoveRegistry(itemB, entryB)
	if err != nil {
		t.Fatalf("backup blob: %v", err)
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, testKey, registry.SET_VALUE)
	err = restoreRegistryValue(k, entryB2.RegValue, entryB2)
	k.Close()
	if err != nil {
		t.Fatal(err)
	}
	k, _ = registry.OpenKey(registry.CURRENT_USER, testKey, registry.QUERY_VALUE)
	gotBin, _, err := k.GetBinaryValue("blob")
	k.Close()
	if err != nil || string(gotBin) != string(bin) {
		t.Fatalf("BINARY 恢复失败: %v got=%v", err, gotBin)
	}
}

// TestScanRogueSmoke 真机冒烟：扫描不 panic、ID 唯一、字段合法
func TestScanRogueSmoke(t *testing.T) {
	svc := &RogueService{}
	items := svc.ScanRogue()

	validTypes := map[string]bool{
		"registry": true, "startup": true, "service": true, "task": true,
		"extension": true, "installed": true, "dir": true, "process": true,
		"shellex": true, "browser": true, "hosts": true, "shortcut": true,
	}
	validActions := map[string]bool{
		"remove": true, "disable_service": true, "disable_task": true,
		"remove_extension": true, "hint": true, "kill": true, "repair": true,
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.ID == "" {
			t.Errorf("空 ID 条目: %+v", it)
		}
		if seen[it.ID] {
			t.Errorf("重复 ID: %s (%s)", it.ID, it.Name)
		}
		seen[it.ID] = true
		if !validTypes[it.RuleType] {
			t.Errorf("非法 RuleType=%q (%s)", it.RuleType, it.Name)
		}
		if !validActions[it.Action] {
			t.Errorf("非法 Action=%q (%s)", it.Action, it.Name)
		}
		// 可清理项必须有可操作的 Path；hint 除外
		if it.Action != "hint" && it.Path == "" {
			t.Errorf("可清理项缺少 Path: %+v", it)
		}
	}
	t.Logf("扫描到 %d 项", len(items))
}
