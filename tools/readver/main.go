// readver 从 build/config.yml 读取 info.version，供构建脚本注入版本号使用。
//
// 用途：Taskfile 中通过 `sh: go run ./tools/readver` 调用，
// 避免依赖 grep/sed 等 Unix 命令（cmd/PowerShell 环境下 task 的 sh 找不到它们）。
//
// 用法：go run ./tools/readver
// 输出：纯版本号字符串（如 0.1.0），无换行。
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// versionLine 匹配 config.yml 中的版本行：`  version: "0.1.0"`
var versionLine = regexp.MustCompile(`^\s*version:\s*"([^"]+)"`)

func main() {
	f, err := os.Open("build/config.yml")
	if err != nil {
		fmt.Fprint(os.Stderr, "readver: 打开 build/config.yml 失败: ", err)
		os.Exit(1)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// 跳过注释行与顶层 `version: '3'`（Taskfile 语法）
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "version:") {
			continue
		}
		if m := versionLine.FindStringSubmatch(line); len(m) > 1 {
			fmt.Print(m[1])
			return
		}
	}
	fmt.Fprint(os.Stderr, "readver: 未在 build/config.yml 中找到 info.version")
	os.Exit(1)
}
