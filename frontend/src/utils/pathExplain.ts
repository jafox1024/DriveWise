/**
 * 常见系统目录/缓存目录解释（面向小白用户）
 * 命中返回解释文案，用于树节点与缓存路径的 ⓘ 提示
 */

const DIR_EXPLAIN: [RegExp, string][] = [
  [/^windows$/i, "Windows 系统目录，存放系统文件，请勿手动删除"],
  [/^winsxs$/i, "Windows 组件存储，保存系统更新/功能组件，由系统自动管理，清理走 DISM"],
  [/^system32$/i, "系统核心程序目录，删除会导致系统无法启动"],
  [/^users$/i, "用户目录，存放所有用户的桌面、文档、下载等个人文件"],
  [/^program files/i, "软件默认安装目录，卸载软件请用「设置 → 应用」"],
  [/^programdata$/i, "程序共享数据目录，部分软件的配置与缓存存放于此"],
  [/^appdata$/i, "应用数据目录，存放各软件配置与缓存（Roaming / Local / LocalLow）"],
  [/^local$/i, "本地应用数据，体积通常较大，多为缓存，可清理"],
  [/^roaming$/i, "漫游应用数据，多为软件配置，删除可能丢失设置"],
  [/^temp$/i, "临时文件目录，可放心清理"],
  [/^prefetch$/i, "程序启动预读取缓存，清理后会自动重新生成，不影响使用"],
  [/^softwaredistribution$/i, "Windows 更新下载目录，其中的 Download 子目录可清理"],
  [/^downloads?$/i, "下载目录，存放你从网络下载的文件，删除前请确认"],
  [/^desktop$/i, "桌面目录，存放桌面上的所有文件"],
  [/^documents?$/i, "文档目录，存放你的个人文档"],
  [/^pictures$/i, "图片目录，存放你的照片与图片"],
  [/^music$/i, "音乐目录，存放你的音乐文件"],
  [/^videos?$/i, "视频目录，存放你的视频文件"],
  [/^\$recycle\.bin$/i, "回收站，删除的文件暂存于此，可从中恢复"],
  [/^recovery$/i, "系统恢复分区数据，请勿删除"],
  [/^pagefile\.sys$/i, "虚拟内存页面文件，由系统管理，删除会导致系统不稳定"],
  [/^hiberfil\.sys$/i, "休眠文件，若不用休眠功能可在电源设置中关闭以释放空间"],
  [/^swapfile\.sys$/i, "系统交换文件，请勿删除"],
  [/^package cache$/i, "安装器缓存（VS 等），删除后部分软件可能无法修复/卸载"],
  [/^npm-cache$|^pnpm-store$|^yarn/i, "前端包管理器缓存，可安全清理，需要时自动重新下载"],
  [/^kingsoft$/i, "金山软件数据目录（WPS 等）"],
  [/^wps office$/i, "WPS Office 安装/升级目录，旧版本残留可清理"],
  [/^google$/i, "Google 软件数据目录（Chrome 等）"],
  [/^microsoft$/i, "Microsoft 软件数据目录（Edge / 系统组件等）"],
  [/^edge$/i, "Microsoft Edge 浏览器数据目录"],
  [/^chromium$/i, "Chromium 内核浏览器数据目录"],
  [/^jetbrains$/i, "JetBrains 开发工具目录（IDEA / GoLand 等）"],
];

/** 根据条目名（或路径）返回解释文案，未命中返回 null */
export function explainPath(nameOrPath: string): string | null {
  if (!nameOrPath) return null;
  // 取最后一段作为目录名（兼容传入完整路径）
  const seg = nameOrPath.replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? nameOrPath;
  for (const [re, text] of DIR_EXPLAIN) {
    if (re.test(seg)) return text;
  }
  return null;
}
