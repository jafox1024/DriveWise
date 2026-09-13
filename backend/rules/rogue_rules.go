package rules

// RogueRule 流氓/推广软件识别规则
// 参考 RogueCleaner 的设计：不依赖模糊子串猜厂商，按维度（路径/注册表值/服务/计划任务/扩展）
// 分别核对；默认全部不勾选，由用户确认后清理。
type RogueRule struct {
	Name      string   // 软件名
	Keywords  []string // 通用关键词（注册表值名/数据/路径/显示名）
	Services  []string // Windows 服务名关键词（匹配服务名与映像路径）
	Tasks     []string // 计划任务名关键词
	ExtIDs    []string // 浏览器扩展 ID / 强制安装策略值关键词
	RiskLevel string   // high | medium | low
	Action    string   // 处理方式: remove | disable_service | disable_task | remove_extension | hint
	Impact    string   // 用户可感知的影响说明
	Note      string   // 说明
}

// RogueRules 内置规则库（默认均不勾选，需用户手动选择后清理）
var RogueRules = []RogueRule{
	// ===== 高风险：浏览器劫持 / 广告弹窗 / 恶意捆绑 =====
	{
		Name:      "2345 全家桶",
		Keywords:  []string{"2345", "2345explorer", "2345pic"},
		Services:  []string{"2345"},
		Tasks:     []string{"2345"},
		ExtIDs:    []string{"2345"},
		RiskLevel: "high",
		Action:    "remove",
		Impact:    "浏览器主页/导航被劫持，弹窗广告，全家桶互装",
		Note:      "2345 浏览器、看图王、压缩等全家桶组件",
	},
	{
		Name:      "浏览器劫持导航",
		Keywords:  []string{"hao123", "qjwm", "2345导航", "browser hijack"},
		ExtIDs:    []string{"hao123", "2345导航"},
		RiskLevel: "high",
		Action:    "remove",
		Impact:    "修改浏览器主页与搜索引擎，强制跳转导航页",
		Note:      "劫持浏览器主页的推广导航组件",
	},
	{
		Name:      "广告弹窗组件",
		Keywords:  []string{"adbox", "adshell", "popad", "adpopup", "winad", "baiduad"},
		RiskLevel: "high",
		Action:    "remove",
		Impact:    "不定时弹出广告窗口，常驻后台",
		Note:      "广告弹窗与守护组件",
	},
	{
		Name:      "挖矿/捆绑下载器",
		Keywords:  []string{"xmrig", "minergate", "bundleware", "downloadhelper"},
		RiskLevel: "high",
		Action:    "remove",
		Impact:    "后台占用 CPU 挖矿或静默下载推广包",
		Note:      "挖矿或捆绑下载组件",
	},

	// ===== 中风险：常见推广全家桶（默认不勾选）=====
	{
		Name:      "WPS/金山推广组件",
		Keywords:  []string{"wps", "kingsoft", "kwservices"},
		Services:  []string{"kwsservice", "kingsoft"},
		RiskLevel: "medium",
		Action:    "remove",
		Impact:    "弹窗推广、捆绑安装其他产品",
		Note:      "WPS 及金山系推广服务，请确认无需使用再清理",
	},
	{
		Name:      "360 推广组件",
		Keywords:  []string{"360safe", "360sd", "360se", "360tray", "zhudongfangyu"},
		Services:  []string{"360", "zhudongfangyu"},
		Tasks:     []string{"360"},
		RiskLevel: "medium",
		Action:    "disable_service",
		Impact:    "开机自启、弹窗推广、全家桶互装",
		Note:      "360 系组件，请确认未使用再清理",
	},
	{
		Name:      "腾讯电脑管家推广",
		Keywords:  []string{"qqpcmgr", "pcmanager", "电脑管家"},
		Services:  []string{"qqpcrtp", "pcmanager"},
		RiskLevel: "medium",
		Action:    "disable_service",
		Impact:    "开机自启、弹窗推广",
		Note:      "腾讯电脑管家组件",
	},
	{
		Name:      "百度系推广",
		Keywords:  []string{"baidu", "baidunetdisk", "百度网盘", "bdbox"},
		RiskLevel: "medium",
		Action:    "remove",
		Impact:    "开机自启、弹窗推广、右键菜单残留",
		Note:      "百度系推广组件（网盘/输入法/手机助手）",
	},
	{
		Name:      "搜狗系推广",
		Keywords:  []string{"sogou", "搜狗", "sgmgr", "inputtools"},
		Services:  []string{"sogou"},
		RiskLevel: "medium",
		Action:    "remove",
		Impact:    "弹窗推广、捆绑安装、浏览器插件残留",
		Note:      "搜狗输入法/浏览器推广组件",
	},
	{
		Name:      "迅雷系推广",
		Keywords:  []string{"thunder", "xunlei", "迅雷", "xlservice"},
		Services:  []string{"xlserviceplatform"},
		RiskLevel: "medium",
		Action:    "disable_service",
		Impact:    "后台常驻、弹窗推广、开机自启",
		Note:      "迅雷下载及播放器组件",
	},
	{
		Name:      "猎豹/金山毒霸",
		Keywords:  []string{"liebao", "ksd", "duba", "金山毒霸", "猎豹"},
		Services:  []string{"ksd", "duba"},
		RiskLevel: "medium",
		Action:    "disable_service",
		Impact:    "弹窗推广、全家桶互装",
		Note:      "金山毒霸/猎豹系组件",
	},
	{
		Name:      "驱动/硬件检测推广",
		Keywords:  []string{"drivergenius", "驱动人生", "ludashi", "鲁大师", "masterlu"},
		Services:  []string{"drivergenius"},
		RiskLevel: "medium",
		Action:    "remove",
		Impact:    "弹窗推广、捆绑安装驱动精灵等",
		Note:      "驱动管家/硬件检测类推广软件",
	},
	{
		Name:      "系统推广入口（C盘瘦身等）",
		Keywords:  []string{"c盘瘦身", "系统瘦身", "一键瘦身", "开机小助手", "加速球", "清理大师"},
		RiskLevel: "medium",
		Action:    "remove",
		Impact:    "在「此电脑/设备和驱动器」或驱动器右键菜单插入推广入口，点击后推送安装自家全家桶",
		Note:      "系统清理/加速类推广入口，常见于安全软件与管家类产品（如 C盘瘦身、加速球）",
	},

	// ===== 低风险：捆绑/提示类 =====
	{
		Name:      "夸克系捆绑",
		Keywords:  []string{"quark", "夸克"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "网盘/浏览器捆绑入口",
		Note:      "夸克网盘/浏览器相关组件",
	},
	{
		Name:      "Bandisoft 捆绑",
		Keywords:  []string{"bandizip", "bandiview", "bandicam"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "安装时捆绑其他软件",
		Note:      "Bandizip/BandiView 等工具捆绑组件",
	},
	{
		Name:      "Flash 中国特供",
		Keywords:  []string{"flash.cn", "flashcenter", "flash 中国"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "广告弹窗、捆绑软件",
		Note:      "Flash 中国特供版组件",
	},
	{
		Name:      "手机助手/设备助手",
		Keywords:  []string{"手机助手", "设备助手", "91助手", "mobilesafe"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "开机自启、推送安装",
		Note:      "手机/设备助手类推广软件",
	},
	{
		Name:      "影音/游戏大厅",
		Keywords:  []string{"风行", "搜狐影音", "游戏大厅", "koowo", "皮皮播放器"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "弹窗广告、捆绑安装",
		Note:      "国产影音/游戏大厅推广软件",
	},
	{
		Name:      "PDF/办公捆绑",
		Keywords:  []string{"pdf快看", "wpspdf", "迅捷pdf", "adobe reader cn"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "捆绑安装、弹窗推广",
		Note:      "PDF/办公类捆绑工具",
	},
	{
		Name:      "预装管家/厂商助手",
		Keywords:  []string{"电脑管家", "联想电脑管家", "厂商助手", "oem助手", "服务中心"},
		RiskLevel: "low",
		Action:    "hint",
		Impact:    "开机自启、弹窗推广",
		Note:      "OEM 预装管家类软件",
	},
}
