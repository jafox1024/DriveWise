package cleaner

import (
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

// ============ 注册表子树备份 / 递归删除 / 按备份还原 ============
//
// 背景（实机复现）：外壳扩展类注册表项常常不止一个「默认值」——
//
//	Drive\shell\{动词}\Command      静态右键动词，命令存在 Command 子键里
//	Drive\shellex\ContextMenuHandlers\{名}  一般只有默认值（= CLSID）
//
// 而 Win32 的 RegDeleteKey 拒绝删除「含子键的键」（返回 Access is denied）。
// 于是出现「此电脑/驱动器里的图标能清理、右键菜单项删不掉」的现象。
//
// 解决：先把整棵子树（所有值 + 所有子键，保留原始类型与字节）序列化备份，
// 再自底向上递归删除；恢复时按备份原样重建，保证可 100% 还原。

// maxKeyTreeDepth 防御异常深/自引用的注册表结构
const maxKeyTreeDepth = 16

// regValueBlob 单个注册表值的原始形态（保留类型与字节，可原样写回）
type regValueBlob struct {
	Kind uint32 `json:"kind"`           // REG_* 类型
	Blob []byte `json:"blob,omitempty"` // 原始字节（JSON 中以 base64 存储）
}

// regKeyNode 注册表子树节点（Values 的键名 "" 表示键的默认值）
type regKeyNode struct {
	Values  map[string]regValueBlob `json:"values,omitempty"`
	SubKeys map[string]*regKeyNode  `json:"subKeys,omitempty"`
}

// dumpKeyTree 递归读取 keyPath 下的全部值与子键，序列化为 JSON 字符串
func dumpKeyTree(hive registry.Key, keyPath string) (string, error) {
	node, err := readKeyNode(hive, keyPath, 0)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(node)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readKeyNode 递归读取单个键节点
func readKeyNode(hive registry.Key, keyPath string, depth int) (*regKeyNode, error) {
	if depth > maxKeyTreeDepth {
		return nil, fmt.Errorf("注册表层级过深，已停止读取: %s", keyPath)
	}
	k, err := registry.OpenKey(hive, keyPath, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, err
	}
	defer k.Close()

	node := &regKeyNode{}
	// GetValue 是低层 API：先传 nil 探测所需字节数与类型，再读数据
	for _, name := range mustValueNames(k) {
		size, kind, verr := k.GetValue(name, nil)
		if verr != nil {
			continue // REG_NONE / 无数据等：跳过（无法原样还原的值不备份）
		}
		data := make([]byte, size)
		if size > 0 {
			if _, _, verr = k.GetValue(name, data); verr != nil {
				continue
			}
		}
		if node.Values == nil {
			node.Values = map[string]regValueBlob{}
		}
		node.Values[name] = regValueBlob{Kind: kind, Blob: data}
	}

	subs, _ := k.ReadSubKeyNames(-1)
	for _, s := range subs {
		child, cerr := readKeyNode(hive, keyPath+`\`+s, depth+1)
		if cerr != nil {
			continue // 个别子键无读权限时尽量保住其余部分
		}
		if node.SubKeys == nil {
			node.SubKeys = map[string]*regKeyNode{}
		}
		node.SubKeys[s] = child
	}
	return node, nil
}

// mustValueNames 读取值名列表（失败返回空）
func mustValueNames(k registry.Key) []string {
	names, _ := k.ReadValueNames(-1)
	return names
}

// deleteKeyTree 自底向上递归删除整棵子树。
// RegDeleteKey 只肯删空键，所以必须先删光所有子键再删自身。
func deleteKeyTree(hive registry.Key, keyPath string) error {
	k, err := registry.OpenKey(hive, keyPath, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return err
	}
	subs, _ := k.ReadSubKeyNames(-1)
	k.Close()
	for _, s := range subs {
		if cerr := deleteKeyTree(hive, keyPath+`\`+s); cerr != nil {
			return fmt.Errorf("删除子键 %s 失败: %w", s, cerr)
		}
	}
	return registry.DeleteKey(hive, keyPath)
}

// restoreKeyTree 按备份 JSON 重建整棵子树（含所有子键与保留原始类型的值）
func restoreKeyTree(hive registry.Key, keyPath, treeJSON string) error {
	var node regKeyNode
	if err := json.Unmarshal([]byte(treeJSON), &node); err != nil {
		return fmt.Errorf("备份数据损坏，无法恢复: %w", err)
	}
	return writeKeyNode(hive, keyPath, &node)
}

// writeKeyNode 递归重建单个键节点
func writeKeyNode(hive registry.Key, keyPath string, node *regKeyNode) error {
	k, _, err := registry.CreateKey(hive, keyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("重建注册表键 %s 失败: %w", keyPath, err)
	}
	for name, v := range node.Values {
		if serr := setRegValueRaw(k, name, v.Kind, v.Blob); serr != nil {
			k.Close()
			return fmt.Errorf("写入值 %q 失败: %w", name, serr)
		}
	}
	k.Close()
	for name, child := range node.SubKeys {
		if werr := writeKeyNode(hive, keyPath+`\`+name, child); werr != nil {
			return werr
		}
	}
	return nil
}

// setRegValueRaw 按原始类型与字节写回注册表值。
// x/sys/windows/registry 未暴露通用的 SetValue（只有 SetStringValue/DWord/…），
// 而备份要「类型+字节」原样还原，故直接调 advapi32!RegSetValueExW。
func setRegValueRaw(k registry.Key, name string, kind uint32, blob []byte) error {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var dataPtr uintptr
	if len(blob) > 0 {
		dataPtr = uintptr(unsafe.Pointer(&blob[0]))
	}
	r0, _, e := procRegSetValueExW.Call(
		uintptr(k), uintptr(unsafe.Pointer(namePtr)), 0,
		uintptr(kind), dataPtr, uintptr(len(blob)))
	if r0 != 0 {
		if e != nil {
			return e
		}
		return fmt.Errorf("RegSetValueEx 失败（错误码 %d）", r0)
	}
	return nil
}

// wrapRegDeleteErr 把底层注册表错误翻译成用户看得懂的原因。
// 这类失败几乎只有两种：HKLM/HKCR 需要提权；或安全软件自我防护拦截。
func wrapRegDeleteErr(hiveName string, err error) error {
	if err == nil {
		return nil
	}
	low := strings.ToLower(err.Error())
	if strings.Contains(low, "access is denied") || strings.Contains(low, "拒绝访问") {
		if hiveName == "HKLM" || hiveName == "HKCR" {
			return fmt.Errorf("权限不足：该注册表项位于 %s，请以管理员身份运行 DriveWise 后重试（若已提权仍失败，多为安全软件的自我防护拦截，需在该软件设置里关闭对应功能）", hiveName)
		}
		return fmt.Errorf("删除被拒绝：该项可能被安全软件自我保护拦截，请在该软件设置中关闭对应功能后重试")
	}
	return err
}
