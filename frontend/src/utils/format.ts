/** 将字节数格式化为人类可读的容量字符串 */
export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  );
  const val = bytes / Math.pow(1024, i);
  const digits = i === 0 || val >= 100 ? 0 : val >= 10 ? 1 : 2;
  return `${val.toFixed(digits)} ${units[i]}`;
}

/** 格式化数量（千分位） */
export function formatCount(n: number): string {
  return (n || 0).toLocaleString("zh-CN");
}
