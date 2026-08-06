package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// TestBlockedPath 受保护路径校验
func TestBlockedPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\`, true},
		{`c:\`, true},
		{`C:\Windows`, true},
		{`c:\windows`, true},
		{`C:\Program Files`, true},
		{`C:\Users`, true},
		{`C:\$Recycle.Bin`, true},
		{`C:\Users\test\AppData`, false},
		{`C:\Windows\Temp\xxx`, false},
		{`D:\`, true},
		{`D:\data`, false},
	}
	for _, c := range cases {
		_, blocked := blockedPath(c.path)
		if blocked != c.want {
			t.Errorf("blockedPath(%q) = %v, want %v", c.path, blocked, c.want)
		}
	}
}

// TestSendToRecycleBin 回收站删除端到端（%TEMP% 测试文件）
func TestSendToRecycleBin(t *testing.T) {
	temp := os.Getenv("TEMP")
	if temp == "" {
		t.Skip("无 TEMP")
	}
	svc := &AnalyzerService{}
	file := filepath.Join(temp, "dwtest_recycle_"+t.Name()+".txt")
	_ = os.WriteFile(file, []byte("recycle test"), 0o644)
	if _, err := os.Lstat(file); err != nil {
		t.Skip("无法创建测试文件")
	}

	_, err := svc.DeletePath(file)
	if err != nil {
		// 诊断：直接调用底层函数
		diagFile := filepath.Join(temp, "dwtest_diag_"+t.Name()+".txt")
		_ = os.WriteFile(diagFile, []byte("x"), 0o644)
		diagErr := sendToRecycleBin(diagFile)
		t.Logf("DeletePath 错误: %v; sendToRecycleBin 直调: %v (存在=%v)", err, diagErr, fileExists(diagFile))
		t.Fatalf("DeletePath: %v", err)
	}
	if _, err := os.Lstat(file); err == nil {
		t.Error("文件应已被移入回收站")
	}

	// 受保护路径必须被拒绝
	if _, err := svc.DeletePath(`C:\Windows`); err == nil {
		t.Error("删除 C:\\Windows 应被拒绝")
	} else if !strings.Contains(err.Error(), "禁止") {
		t.Errorf("错误信息应为禁止提示: %v", err)
	}
}
