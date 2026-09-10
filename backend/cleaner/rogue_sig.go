package cleaner

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows/registry"

	"drivewise/backend/internal/sigcheck"
	"drivewise/backend/internal/winutil"
	"drivewise/backend/models"
	"drivewise/backend/rules"
)

// ============ 数字签名黑名单扫描 ============
//
// softcnkiller 的核心识别方式：读取候选文件的 Authenticode 数字签名者，
// 与 sign.txt 发布者黑名单精确比对。本项目此前只把 sign.txt 用于
// Uninstall 注册表的 Publisher 字符串匹配（可被伪造/为空），这里补上
// 对 运行中进程 / 注册表启动项 / 服务映像路径 所指向的真实 exe 文件验签，
// 命中即列为可疑项（可执行清理）。
//
// 清理时的二次扫描基于注册表/进程/服务实时状态，ID 用 exe 路径保证稳定，
// 不会因 PID 变化导致“未找到对应项目”。

// signerCache 缓存 exe 路径→签名者，避免同一文件重复验签（进程多实例/多处引用）
var signerCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// signerOf 返回 exe 的签名者（失败/未签名返回 ""），带缓存
func signerOf(exePath string) string {
	key := strings.ToLower(filepath.Clean(exePath))
	signerCache.Lock()
	v, ok := signerCache.m[key]
	signerCache.Unlock()
	if ok {
		return v
	}
	name, err := sigcheck.Signer(exePath)
	if err != nil {
		name = ""
	}
	signerCache.Lock()
	if len(signerCache.m) > 4096 {
		signerCache.m = map[string]string{} // 简单防膨胀
	}
	signerCache.m[key] = name
	signerCache.Unlock()
	return name
}

// blacklistedSigner 签名者命中发布者黑名单则返回其小写名称
func blacklistedSigner(signer string) string {
	if signer == "" {
		return ""
	}
	if rules.IsBlacklistedPublisher(signer) {
		return strings.ToLower(signer)
	}
	return ""
}

// isSystemExePath 系统目录下的 exe 不做签名扫描（微软签名不可能命中黑名单，省 IO）
func isSystemExePath(p string) bool {
	low := strings.ToLower(p)
	return strings.HasPrefix(low, `c:\windows\`) || strings.Contains(low, `\microsoft\`)
}

// exeCandidate 待验签的可执行文件及其来源信息
type exeCandidate struct {
	exePath  string // 待验签的 exe 完整路径
	ruleType string // 来源维度: process | registry | service
	name     string // 展示名
	location string // 位置描述
	path     string // 清理用主键（注册表完整路径/服务名/exe 路径）
	value    string // 辅助值（注册表值名 / 服务显示名 / exe 名）
	action   string // 对应清理动作
}

// extractExePath 从启动命令/服务映像路径中解析出首个 exe 完整路径
func extractExePath(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	cmd = winutil.ExpandEnv(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if idx := strings.IndexByte(cmd[1:], '"'); idx >= 0 {
			cmd = cmd[1 : 1+idx]
		}
	} else if idx := strings.IndexAny(cmd, " /"); idx > 0 {
		cmd = cmd[:idx]
	}
	cmd = strings.Trim(cmd, `"`)
	if cmd == "" {
		return ""
	}
	low := strings.ToLower(cmd)
	if !strings.HasSuffix(low, ".exe") && !strings.HasSuffix(low, ".com") && !strings.HasSuffix(low, ".dll") {
		return ""
	}
	if isSystemExePath(cmd) || rules.IsWhitelistedPath(cmd) {
		return ""
	}
	if _, err := os.Lstat(cmd); err != nil {
		return ""
	}
	return cmd
}

// collectRunKeyExes 收集注册表启动项指向的 exe（关键词规则未命中的才验签，避免重复项）
func collectRunKeyExes() []exeCandidate {
	var out []exeCandidate
	for _, t := range runKeys {
		hivePrefix := "HKLM"
		if t.hive == registry.CURRENT_USER {
			hivePrefix = "HKCU"
		}
		k, err := registry.OpenKey(t.hive, t.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		names, _ := k.ReadValueNames(-1)
		for _, name := range names {
			data, _, err := k.GetStringValue(name)
			if err != nil {
				continue
			}
			if matchRule(name + " " + data) != nil {
				continue // 已被关键词规则命中
			}
			exe := extractExePath(data)
			if exe == "" {
				continue
			}
			regPath := hivePrefix + `\` + t.path + `\` + name
			out = append(out, exeCandidate{
				exePath:  exe,
				ruleType: "registry",
				name:     filepath.Base(exe),
				location: t.desc + "（签名黑名单）",
				path:     regPath,
				value:    data,
				action:   "remove",
			})
		}
		k.Close()
	}
	return out
}

// collectServiceExes 收集服务映像路径指向的 exe
func collectServiceExes() []exeCandidate {
	var out []exeCandidate
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, sub := range subs {
		sk, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+sub, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		display, _, _ := sk.GetStringValue("DisplayName")
		img, _, _ := sk.GetStringValue("ImagePath")
		sk.Close()
		if matchService(sub, display+" "+img) != nil {
			continue // 已被关键词规则命中
		}
		exe := extractExePath(img)
		if exe == "" {
			continue
		}
		out = append(out, exeCandidate{
			exePath:  exe,
			ruleType: "service",
			name:     filepath.Base(exe),
			location: "Windows 服务（签名黑名单）",
			path:     sub,
			value:    display,
			action:   "disable_service",
		})
	}
	return out
}

// collectProcessExes 收集位于用户可写目录的运行中进程 exe（仅未命中关键词/黑名单目录的）
func collectProcessExes() []exeCandidate {
	userRoots := map[string]bool{}
	for _, env := range []string{"LOCALAPPDATA", "APPDATA", "TEMP", "PROGRAMDATA"} {
		if v := os.Getenv(env); v != "" {
			userRoots[strings.ToLower(filepath.Clean(v))] = true
		}
	}
	if len(userRoots) == 0 {
		return nil
	}
	var out []exeCandidate
	procs := snapshotProcesses()
	seen := map[string]bool{}
	for _, p := range procs {
		if p.path == "" || p.name == "" || isSystemProcess(p.name) || isSystemExePath(p.path) {
			continue
		}
		if matchHighRisk(p.name+" "+p.path) != nil {
			continue // 已被高置信关键词命中
		}
		dir := strings.ToLower(filepath.Dir(p.path))
		inUserDir := false
		for root := range userRoots {
			if dir == root || strings.HasPrefix(dir, root+`\`) {
				inUserDir = true
				break
			}
		}
		if !inUserDir {
			continue
		}
		// 已被目录黑名单命中的进程跳过（scanProcesses 已处理）
		if rules.IsBlacklistedDir(filepath.Base(filepath.Dir(p.path))) || rules.IsBlacklistedPath(p.path) {
			continue
		}
		low := strings.ToLower(p.path)
		if seen[low] {
			continue
		}
		seen[low] = true
		out = append(out, exeCandidate{
			exePath:  p.path,
			ruleType: "process",
			name:     p.name,
			location: "运行中进程（签名黑名单）",
			path:     p.path,
			value:    p.name,
			action:   "kill",
		})
	}
	return out
}

// scanSignedFiles 对候选 exe 验签，签名者命中发布者黑名单的列为可疑项
func scanSignedFiles() []models.RogueItem {
	cands := collectRunKeyExes()
	cands = append(cands, collectServiceExes()...)
	cands = append(cands, collectProcessExes()...)

	var items []models.RogueItem
	seen := map[string]bool{}
	for _, c := range cands {
		if c.exePath == "" || seen[strings.ToLower(c.exePath)] {
			continue
		}
		seen[strings.ToLower(c.exePath)] = true
		pub := blacklistedSigner(signerOf(c.exePath))
		if pub == "" {
			continue
		}
		items = append(items, models.RogueItem{
			ID:        genID("sign", c.ruleType, c.exePath),
			Name:      c.name,
			RuleType:  c.ruleType,
			Location:  c.location,
			Path:      c.path,
			Value:     c.value,
			RiskLevel: "medium",
			Detail:    "数字签名者「" + pub + "」命中发布者黑名单（sign.txt）",
			Impact:    "文件由已知流氓软件厂商签名，可能存在弹窗/捆绑/广告行为",
			Action:    c.action,
			Selected:  false,
		})
	}
	return items
}
