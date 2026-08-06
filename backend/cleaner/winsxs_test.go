package cleaner

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestDecodeDismOutput 验证 GBK 输出能正确解码为 UTF-8
func TestDecodeDismOutput(t *testing.T) {
	// 模拟中文 Windows 上 dism 输出（GBK 编码的“部署映像服务和管理工具”）
	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("部署映像服务和管理工具\n正在处理 [1/100]..."))
	got := decodeDismOutput(gbk)
	if got != "部署映像服务和管理工具\n正在处理 [1/100]..." {
		t.Errorf("GBK 解码失败: %q", got)
	}

	// UTF-8 输入原样返回
	if s := decodeDismOutput([]byte("Component Store Size : 5.2 GB")); s != "Component Store Size : 5.2 GB" {
		t.Errorf("UTF-8 输入被改动: %q", s)
	}
}
