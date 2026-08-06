// Package winutil 提供 Windows 平台通用工具函数
package winutil

import (
	"os"
	"regexp"

	"golang.org/x/sys/windows"
)

var envPattern = regexp.MustCompile(`%([^%]+)%`)

// ExpandEnv 展开字符串中的 %VAR% 形式环境变量（Windows 风格），
// 同时兼容 $VAR 与 ${VAR}（os.Expand 语法）。
func ExpandEnv(s string) string {
	s = os.Expand(s, os.Getenv)
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		key := envPattern.FindStringSubmatch(m)[1]
		return os.Getenv(key)
	})
}

// IsAdmin 检测当前进程是否以管理员权限（elevated）运行
func IsAdmin() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

// IsNTFS 检测卷是否为 NTFS 文件系统（MFT 扫描前提）
func IsNTFS(volumeRoot string) bool {
	ptr, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return false
	}
	var fsName [64]uint16
	err = windows.GetVolumeInformation(
		ptr, nil, 0, nil, nil, nil,
		&fsName[0], uint32(len(fsName)),
	)
	if err != nil {
		return false
	}
	return windows.UTF16ToString(fsName[:]) == "NTFS"
}
