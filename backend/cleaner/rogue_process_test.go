package cleaner

import (
	"strings"
	"testing"
)

// TestSnapshotProcesses 进程快照（真实环境）
func TestSnapshotProcesses(t *testing.T) {
	procs := snapshotProcesses()
	if len(procs) == 0 {
		t.Fatal("进程快照为空")
	}
	hasSystem := false
	for _, p := range procs {
		if strings.EqualFold(p.name, "System") {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Log("未捕获 System 进程（可能权限受限），快照数:", len(procs))
	}
	t.Logf("进程数: %d", len(procs))
}

// TestScanProcesses 进程扫描不崩溃且跳过系统进程
func TestScanProcesses(t *testing.T) {
	items := scanProcesses()
	for _, it := range items {
		if isSystemProcess(it.Value) {
			t.Errorf("系统进程不应命中: %s", it.Value)
		}
		if !it.Running {
			t.Errorf("进程项应标记运行中: %s", it.Value)
		}
	}
	t.Logf("命中运行中进程 %d 个", len(items))
	for _, it := range items {
		t.Logf("  - %s (PID=%d) %s", it.Value, it.PID, it.Path)
	}
}

// TestScanShellEx Shell 扩展扫描不崩溃
func TestScanShellEx(t *testing.T) {
	items := scanShellEx()
	t.Logf("Shell 扩展命中 %d 个", len(items))
	for _, it := range items {
		t.Logf("  - %s | %s | %s", it.Name, it.Location, it.Value)
	}
}

// TestResolveCLSIDServer 已知 CLSID 解析
func TestResolveCLSIDServer(t *testing.T) {
	// 系统自带 CLSID（Unknown 类）
	path := resolveCLSIDServer("{00021401-0000-0000-C000-000000000046}")
	if path == "" {
		t.Log("CLSID 解析为空（可能权限）")
	} else {
		t.Logf("解析到: %s", path)
	}
}
