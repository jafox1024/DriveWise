# DriveWise 🚀

**C 盘智能清理工具** —— 基于 Wails v3 + Vue 3 + TypeScript 构建的 Windows 桌面应用。

## 功能特性

| 模块 | 状态 | 说明 |
|------|------|------|
| 📊 磁盘分析 | ✅ 已完成 | 树形目录（每层按大小降序）+ 多引擎（MFT 极速 / 遍历精确）+ **右键打开目录 / 移入回收站**（删除后动态刷新）+ **常见目录小白解释 ⓘ** |
| 🧹 缓存清理 | ✅ 已完成 | 6 类缓存扫描清理 + 详情/针对性勾选 + **WinSxS 后台清理**（不阻塞界面）+ **软件升级残留专项**（WPS/浏览器旧版本、安装包缓存）+ **分类说明文案** |
| 📦 软件迁移 | ✅ 已完成 | 应用复制迁移 + 目录联接，支持恢复 |
| 🦠 流氓软件 | ✅ 已完成 | **参考 RogueCleaner + softcnkiller 黑名单**：服务/计划任务/浏览器扩展/启动项多维扫描 + 1000+ 目录黑名单 + 389 签名黑名单，默认不勾选，备份可恢复 |
| ⚡ 通用 | ✅ 已完成 | **管理员权限检测 + UAC 提权重启**；并发扫描性能优化 |

## 技术栈

- **框架**: Wails v3 (beta.3)
- **前端**: Vue 3 + TypeScript + Vite 8
- **UI**: Tailwind CSS v4 + Pinia + ECharts 6
- **后端**: Go 1.25

## 项目结构

```
DriveWise/
├── main.go                 # 应用入口（窗口 + 服务注册）
├── backend/
│   ├── analyzer/           # 磁盘分析（并发扫描 + 文件树）
│   ├── cleaner/cache.go    # 缓存清理（ScanCache/CleanCache）
│   ├── cleaner/cache_detail.go # 缓存详情 + 针对性清理（白名单校验）
│   ├── cleaner/winsxs.go   # WinSxS 组件存储清理（dism 封装 + 中英文解析）
│   ├── cleaner/rogue.go    # 流氓软件清理（服务/任务/扩展/启动项/黑名单目录多维扫描）
│   ├── rules/              # 流氓软件规则库（厂商/服务/任务/扩展四维匹配）
│   ├── rules/blacklist.go  # softcnkiller 黑名单加载与匹配（go:embed 数据）
│   ├── migrator/           # 软件迁移（复制 + 目录联接 + 迁移记录）
│   ├── sysinfo/            # 系统信息（管理员检测 + UAC 提权重启）
│   ├── models/             # 共享数据模型
│   └── internal/
│       ├── fsutil/         # 并发目录大小计算器
│       └── winutil/        # Windows 工具函数（%VAR% 环境变量展开等）
├── frontend/               # Vue 3 前端
│   ├── src/
│   │   ├── pages/          # 四大功能页面
│   │   ├── stores/         # Pinia 状态管理
│   │   ├── components/     # UI 组件
│   │   └── utils/          # 工具函数
│   └── bindings/           # Wails 自动生成的前端绑定（勿手改）
├── build/                  # 构建配置
└── bin/                    # 编译产物
```

## 开发

```bash
# 开发模式（热重载）
wails3 dev

# 生产构建
wails3 build

# 仅编译后端
go build drivewise

# 重新生成前端绑定（新增/修改服务后执行）
wails3 generate bindings -ts
```

> 注：缓存清理与注册表清理涉及系统目录，部分路径需要管理员权限。

## 安全设计

- **缓存清理**：目标路径固定在后端定义，前端仅传分类名；不跟随目录联接；只清内容不删根目录；被占用文件跳过并记录
- **缓存详情清理**：条目路径必须位于该分类已解析路径白名单内（大小写不敏感、禁止删除根本身），越权路径一律拒绝
- **软件升级残留**：自动识别 WPS/Chrome/Edge/Chromium 多版本目录并**保留最新版本**；WPS 组件池（addons\pool）按组件解析版本仅清理旧版；Package Cache、WPS jsaddons 与 updater 大目录（>50MB）需在详情中勾选删除；整类清理只删旧版本
- **WinSxS 清理**：调用系统 DISM（`/StartComponentCleanup`），**后台 goroutine 执行 + 状态轮询**（`StartWinSxSClean` / `GetWinSxSStatus`），不阻塞界面；中英文/GBK 输出均可解析显示；需管理员权限
- **流氓软件清理**（参考 RogueCleaner）：扫描注册表启动项/启动文件夹/服务/计划任务/浏览器强制扩展/已装软件；**默认全部不勾选**；服务采用「备份启动类型→停止→禁用」，任务采用「备份 XML→禁用」，注册表/扩展值删除前备份；恢复中心按批次回滚；「仅提示」类项目不参与一键清理
- **softcnkiller 黑名单**：内置 1000+ 流氓目录名、69 条相对路径、389 条发布者签名黑名单（go:embed 数据文件）；**支持在线更新**（一键从 gitee 拉取最新数据，本地优先加载 + 热重载，无需重新编译）；发布者命中签名黑名单的报告为「仅提示」，安装目录命中黑名单的排除系统保留目录后报告
- **软件迁移**：先复制并校验 → 原目录改名 → 创建联接 → 删除备份，任一步失败自动回滚；迁移记录持久化，可随时恢复
- **磁盘分析**：树形按需加载（每层 50 项），跳过符号链接/联接避免环路；无权限目录静默跳过
- **MFT 扫描**：通过 `FSCTL_GET_NTFS_VOLUME_DATA` 定位 MFT 后直接解析主文件表（含 USN 修复、$ATTRIBUTE_LIST 扩展记录补全），秒级完成全盘统计；按文件记录去重（硬链接不重复计），$BadClus 等系统元数据排除；需管理员权限，非 NTFS/非管理员自动回退目录遍历
- **删除保护**：磁盘分析右键删除一律**移入回收站**（PowerShell VisualBasic API，非永久删除）；禁止删除磁盘根目录与系统关键目录（Windows/Program Files/Users 等）；删除前二次确认并提示
- **管理员权限**：启动时检测是否已提权，非管理员显示横幅引导一�� UAC 提权重启（`SysService.RestartAsAdmin`）

## 已实现后端服务 API

| 服务 | 方法 |
|------|------|
| AnalyzerService | GetDrives / ScanDirectory / ScanDisk / ScanMFT / GetDirChildren / **OpenInExplorer** / **DeletePath** |
| CacheService | ScanCache / CleanCache / ScanCacheDetails / CleanCacheItems / AnalyzeWinSxS / **StartWinSxSClean** / **GetWinSxSStatus** |
| RogueService | ScanRogue / CleanRogue / RestoreRogue / GetBackups / **GetBlacklistInfo** / **UpdateBlacklist** |
| MigratorService | ScanApps / MoveApp / RestoreApp / GetMigrations |
| SysService | IsAdmin / RestartAsAdmin |
