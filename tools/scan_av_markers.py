import sys

p = r"E:\git\DriveWise\bin\drivewise.exe"
d = open(p, "rb").read()
print("size = %d bytes (%.2f MB)" % (len(d), len(d) / 1048576))

# 应被清除的"杀软高危特征串"
must_be_gone = [
    "-ExecutionPolicy",
    "ExecutionPolicy",
    "Bypass",
    "Start-Process",
    "Add-Type",
    "Microsoft.VisualBasic",
    "EncodedCommand",
    "Net.WebClient",
    "DownloadString",
]

# 采集用：仍会存在的字符串（用于确认扫描逻辑有效、并了解剩余特征面）
observed = [
    "New-Object",
    "WScript.Shell",
    "CreateShortcut",
    "shell",
    "runas",
    "ShellExecuteExW",
    "SHFileOperationW",
    "TerminateProcess",
    "MOVEFILE_DELAY_UNTIL_REBOOT",
    "schtasks",
    "takeown",
    "icacls",
    "dism.exe",
    "mklink",
    "DeviceIoControl",
]


def count_ascii(s):
    return d.count(s.encode("latin-1", errors="ignore"))


def count_wide(s):
    return d.count(s.encode("utf-16-le", errors="ignore"))


print("\n--- 必须已清除的特征串 ---")
bad = 0
for m in must_be_gone:
    a, w = count_ascii(m), count_wide(m)
    status = "已清除" if a == 0 and w == 0 else "仍存在 (ascii=%d, utf16=%d)" % (a, w)
    if a or w:
        bad += 1
    print("  %-26s %s" % (m, status))

print("\n--- 当前仍存在的字符串（功能所需 / 供评估）---")
for m in observed:
    a, w = count_ascii(m), count_wide(m)
    if a or w:
        print("  %-30s ascii=%-4d utf16=%d" % (m, a, w))

print("\n结论: %s" % ("全部高危特征串已清除" if bad == 0 else "仍有 %d 个高危特征串" % bad))
