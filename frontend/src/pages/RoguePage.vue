<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { RogueService } from "../../bindings/drivewise/backend/cleaner";
import { AnalyzerService } from "../../bindings/drivewise/backend/analyzer";
import { SysService } from "../../bindings/drivewise/backend/sysinfo";
import type { BlacklistInfo, RogueItem } from "../../bindings/drivewise/backend/models";
import type { BackupEntry } from "../../bindings/drivewise/backend/cleaner/models";
import AppModal from "../components/AppModal.vue";

const props = defineProps<{ isAdmin?: boolean }>();

const items = ref<RogueItem[]>([]);
const backups = ref<BackupEntry[]>([]);
const scanning = ref(false);
const cleaning = ref(false);
const restoring = ref(false);
const restarting = ref(false);
const message = ref<{ type: "ok" | "err"; text: string } | null>(null);
const showBackups = ref(false);

/* ========== 单条「清理该项」确认弹窗 ========== */
const itemConfirmOpen = ref(false);
const itemDeleting = ref(false);
const pendingItem = ref<RogueItem | null>(null);

/** 有真实本地文件/目录路径、可“打开位置”的类型 */
const openableTypes = new Set(["dir", "startup", "file", "hosts", "shortcut", "process"]);
function canOpen(item: RogueItem): boolean {
  return openableTypes.has(item.ruleType) && !!item.path;
}

/** 当前是否为管理员（未检测完成前按 true 处理，避免闪烁误导） */
const isAdmin = computed(() => props.isAdmin !== false);

/* 黑名单更新状态 */
const blacklist = ref<BlacklistInfo | null>(null);
const updating = ref(false);
const updateMsg = ref("");

const selectedCount = computed(() => items.value.filter((i) => i.selected).length);

/** 需要管理员才能执行的类型（HKLM/系统目录） */
const adminRuleTypes = new Set(["service", "task", "shellex", "dir", "hosts"]);
/** 需管理员判断：类型命中，或注册表路径在 HKLM/HKCR（写系统配置需提权） */
function needsAdmin(item: RogueItem): boolean {
  if (adminRuleTypes.has(item.ruleType)) return true;
  if (item.ruleType === "registry" || item.ruleType === "extension" || item.ruleType === "browser") {
    const p = item.path || "";
    if (p.startsWith("HKLM") || p.startsWith("HKCR")) return true;
  }
  // 公共桌面等系统共享位置的快捷方式修复需要管理员
  if (item.ruleType === "shortcut" && (item.path || "").includes("Users\\Public")) return true;
  return false;
}
/** 仅提示、不可一键清理 */
function isHint(item: RogueItem): boolean {
  return item.action === "hint";
}
/** 该勾选项说明：仅提示 / 需管理员 / 可清理 */
function checkTip(item: RogueItem): string {
  if (isHint(item)) return "此项目仅提示，不参与一键清理，请按说明在系统设置中处理";
  if (needsAdmin(item)) return "此操作需要管理员权限，普通权限下会失败";
  return "";
}

async function restartAsAdmin() {
  restarting.value = true;
  try {
    await SysService.RestartAsAdmin();
  } catch (e) {
    restarting.value = false;
    message.value = { type: "err", text: `提权重启失败：${String(e)}` };
  }
}

onMounted(async () => {
  try {
    blacklist.value = await RogueService.GetBlacklistInfo();
  } catch {
    /* 忽略 */
  }
});

async function updateBlacklist() {
  updating.value = true;
  updateMsg.value = "";
  try {
    const res = await RogueService.UpdateBlacklist();
    const files = res.files ?? [];
    const ok = files.filter((f) => f.ok);
    const fail = files.filter((f) => !f.ok);
    updateMsg.value = fail.length === 0
      ? `✅ 黑名单已更新：${ok.map((f) => `${f.name} ${f.count} 条`).join("、")}`
      : `⚠️ 部分更新成功（${ok.length}/${files.length}）：${fail.map((f) => `${f.name}: ${f.err}`).join("；")}`;
    blacklist.value = await RogueService.GetBlacklistInfo();
  } catch (e) {
    updateMsg.value = `更新失败：${String(e)}`;
  } finally {
    updating.value = false;
  }
}

const riskStyle: Record<string, string> = {
  high: "bg-red-50 text-red-600 border-red-200",
  medium: "bg-amber-50 text-amber-600 border-amber-200",
  low: "bg-slate-100 text-slate-500 border-slate-200",
};
const riskLabel: Record<string, string> = {
  high: "高风险",
  medium: "中风险",
  low: "低风险",
};

const actionStyle: Record<string, string> = {
  remove: "bg-red-50 text-red-600",
  kill: "bg-red-50 text-red-600",
  disable_service: "bg-orange-50 text-orange-600",
  disable_task: "bg-orange-50 text-orange-600",
  remove_extension: "bg-purple-50 text-purple-600",
  repair: "bg-emerald-50 text-emerald-600",
  hint: "bg-slate-100 text-slate-500",
};
const actionLabel: Record<string, string> = {
  remove: "删除",
  kill: "结束进程",
  disable_service: "禁用服务",
  disable_task: "禁用任务",
  remove_extension: "移除扩展",
  repair: "修复",
  hint: "仅提示",
};

async function scan() {
  scanning.value = true;
  message.value = null;
  try {
    items.value = (await RogueService.ScanRogue()) ?? [];
  } catch (e) {
    message.value = { type: "err", text: `扫描失败：${String(e)}` };
  } finally {
    scanning.value = false;
  }
}

async function clean() {
  const selected = items.value.filter((i) => i.selected);
  if (!selected.length) return;
  cleaning.value = true;
  message.value = null;
  try {
    const results = (await RogueService.CleanRogueItems(selected)) ?? [];
    showCleanResults(results);
    // 清理后自动复扫（对抗自动重装/自愈的流氓软件，检查残留）
    await scan();
    await loadBackups();
  } catch (e) {
    message.value = { type: "err", text: `清理失败：${String(e)}` };
  } finally {
    cleaning.value = false;
  }
}

/** 展示批量/单条清理结果汇总 */
function showCleanResults(results: { ok: boolean; errMsg?: string }[]) {
  const ok = results.filter((r) => r.ok);
  const failed = results.filter((r) => !r.ok);
  if (failed.length === 0) {
    message.value = { type: "ok", text: `已清理 ${ok.length} 项，可在「恢复记录」中找回` };
  } else if (ok.length === 0) {
    const reasons = [...new Set(failed.map((f) => f.errMsg))].slice(0, 3);
    message.value = {
      type: "err",
      text: `${failed.length} 项清理失败：${reasons.join("；")}${failed.length > 3 ? "…" : ""}`,
    };
  } else {
    const reasons = [...new Set(failed.map((f) => f.errMsg))].slice(0, 2);
    message.value = {
      type: "err",
      text: `已清理 ${ok.length} 项，${failed.length} 项失败：${reasons.join("；")}`,
    };
  }
}

/** 打开该条对应的目录 / 文件所在位置（借助磁盘分析页的打开实现） */
async function openLocation(item: RogueItem) {
  message.value = null;
  try {
    await AnalyzerService.OpenInExplorer(item.path);
  } catch (e) {
    message.value = { type: "err", text: `打开失败：${String(e)}` };
  }
}

/** 请求单条清理（弹确认） */
function requestItemClean(item: RogueItem) {
  if (item.action === "hint") return;
  pendingItem.value = item;
  itemConfirmOpen.value = true;
}

/** 执行单条清理（dir/file/startup 会先结束占用进程再备份隔离，可恢复） */
async function confirmItemClean() {
  const item = pendingItem.value;
  if (!item) return;
  itemDeleting.value = true;
  message.value = null;
  try {
    const results = (await RogueService.CleanRogueItems([item])) ?? [];
    showCleanResults(results);
    if (results[0]?.ok) {
      items.value = items.value.filter((i) => i.id !== item.id);
    }
    await loadBackups();
  } catch (e) {
    message.value = { type: "err", text: `清理失败：${String(e)}` };
  } finally {
    itemDeleting.value = false;
    itemConfirmOpen.value = false;
    pendingItem.value = null;
  }
}

async function loadBackups() {
  backups.value = (await RogueService.GetBackups()) ?? [];
}

async function restore(id: string) {
  restoring.value = true;
  message.value = null;
  try {
    const results = (await RogueService.RestoreRogue([id])) ?? [];
    if (results[0]?.ok) {
      message.value = { type: "ok", text: "已恢复" };
      await loadBackups();
    } else {
      message.value = {
        type: "err",
        text: `恢复失败：${results[0]?.errMsg || "请确认以管理员身份运行后重试"}`,
      };
    }
  } catch (e) {
    message.value = { type: "err", text: `恢复失败：${String(e)}` };
  } finally {
    restoring.value = false;
  }
}
</script>

<template>
  <div class="mx-auto max-w-5xl p-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold text-slate-800">流氓软件清理</h1>
      <p class="mt-1 text-sm text-slate-500">
        扫描启动项、服务、计划任务、浏览器主页/扩展劫持、隐蔽自启点（Userinit 等）、Hosts 篡改与快捷方式参数注入，识别推广/广告/捆绑组件。所有项目默认不勾选，清理前先备份，可随时恢复。
      </p>
    </header>

    <!-- 非管理员提示 -->
    <div
      v-if="!isAdmin"
      class="mb-5 flex items-center justify-between gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700"
    >
      <div>
        <span class="font-medium">当前为普通权限：</span>
        禁用服务、计划任务、HKLM 注册表启动项与系统目录清理都需要管理员权限，普通权限下这些项目会清理失败。可先扫描查看，再以管理员身份重启后清理。
      </div>
      <button
        class="shrink-0 rounded-lg bg-amber-500 px-4 py-1.5 text-xs font-medium text-white transition-colors hover:bg-amber-600 disabled:cursor-not-allowed disabled:opacity-60"
        :disabled="restarting"
        @click="restartAsAdmin"
      >
        {{ restarting ? "正在请求提权..." : "以管理员身份重启" }}
      </button>
    </div>

    <!-- 黑名单状态与更新 -->
    <div class="mb-5 rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="text-sm text-slate-600">
          <span class="font-semibold text-slate-700">黑名单库</span>
          <template v-if="blacklist">
            <span class="ml-2 text-xs text-slate-400">
              目录 {{ blacklist.dirs }} · 路径 {{ blacklist.paths }} · 签名 {{ blacklist.signs }} · 白名单 {{ blacklist.white }}
            </span>
            <span v-if="blacklist.localTime" class="ml-2 rounded bg-emerald-50 px-1.5 py-0.5 text-[10px] text-emerald-600">
              已更新 {{ blacklist.localTime.slice(0, 16).replace("T", " ") }}
            </span>
            <span v-else class="ml-2 rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-400">使用内置规则</span>
          </template>
          <span v-else class="ml-2 text-xs text-slate-400">加载中...</span>
        </div>
        <div class="flex items-center gap-3">
          <span v-if="updateMsg" class="max-w-md text-xs" :class="updateMsg.startsWith('✅') ? 'text-emerald-600' : 'text-red-500'">
            {{ updateMsg }}
          </span>
          <button
            class="rounded-lg border border-violet-300 px-4 py-1.5 text-sm font-medium text-violet-700 transition-colors hover:bg-violet-50 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="updating"
            @click="updateBlacklist"
          >
            {{ updating ? "更新中..." : "🔄 更新黑名单" }}
          </button>
        </div>
      </div>
    </div>

    <!-- 操作栏 -->
    <div class="mb-5 flex items-center justify-between rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <div class="flex items-center gap-3">
        <button
          class="rounded-lg bg-blue-600 px-5 py-2 text-sm font-medium text-white shadow-sm shadow-blue-200 transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="scanning || cleaning"
          @click="scan"
        >
          {{ scanning ? "扫描中..." : "开始扫描" }}
        </button>
        <button
          class="rounded-lg bg-red-600 px-5 py-2 text-sm font-medium text-white shadow-sm shadow-red-200 transition-colors hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="cleaning || selectedCount === 0"
          @click="clean"
        >
          {{ cleaning ? "清理中..." : `清理选中 (${selectedCount})` }}
        </button>
      </div>
      <button
        class="text-sm text-slate-500 hover:text-slate-700"
        @click="showBackups = !showBackups; if (showBackups) loadBackups()"
      >
        {{ showBackups ? "收起恢复记录" : "恢复记录" }}
      </button>
    </div>

    <!-- 提示 -->
    <div
      v-if="message"
      class="mb-5 rounded-xl border p-4 text-sm shadow-sm"
      :class="message.type === 'ok' ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : 'border-red-200 bg-red-50 text-red-700'"
    >
      {{ message.text }}
    </div>

    <!-- 恢复记录 -->
    <div v-if="showBackups" class="mb-5 rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <div class="mb-3 text-sm font-semibold text-slate-700">恢复记录（共 {{ backups.length }} 条）</div>
      <div v-if="backups.length === 0" class="py-4 text-center text-sm text-slate-400">
        暂无备份记录
      </div>
      <ul class="space-y-2">
        <li
          v-for="b in backups"
          :key="b.id"
          class="flex items-center justify-between gap-3 rounded-lg bg-slate-50 px-3 py-2 text-sm"
        >
          <div class="min-w-0 flex-1">
            <span class="font-medium text-slate-700">{{ b.name }}</span>
            <span class="ml-2 text-xs text-slate-400">{{ b.time }}</span>
            <div class="truncate text-xs text-slate-400">
              <template v-if="b.ruleType === 'service'">服务：{{ b.serviceName }}</template>
              <template v-else-if="b.ruleType === 'task'">计划任务：{{ b.taskName }}</template>
              <template v-else-if="b.ruleType === 'registry' || b.ruleType === 'extension' || b.ruleType === 'browser'">{{ b.regHive }}\{{ b.regKey }}\{{ b.regValue }}</template>
              <template v-else>{{ b.origPath }}</template>
            </div>
          </div>
          <button
            class="shrink-0 rounded-md border border-slate-300 px-3 py-1 text-xs text-slate-600 hover:bg-slate-100 disabled:opacity-50"
            :disabled="restoring"
            @click="restore(b.id)"
          >
            恢复
          </button>
        </li>
      </ul>
    </div>

    <!-- 扫描结果 -->
    <div class="space-y-3">
      <div
        v-if="items.length === 0 && !scanning"
        class="flex flex-col items-center justify-center rounded-xl border-2 border-dashed border-slate-200 bg-white py-20 text-slate-400"
      >
        <span class="mb-3 text-4xl">🦠</span>
        <p class="text-sm">点击「开始扫描」检测推广与广告组件</p>
      </div>

      <div
        v-for="item in items"
        :key="item.id"
        class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
      >
        <div class="flex items-center gap-3">
          <input
            type="checkbox"
            v-model="item.selected"
            :disabled="isHint(item)"
            :title="checkTip(item)"
            class="h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500 disabled:cursor-not-allowed disabled:opacity-40"
          />
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-semibold text-slate-700">{{ item.name }}</span>
              <span
                v-if="isHint(item)"
                class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] font-medium text-slate-500"
                title="此项目仅提示，不参与一键清理，请按说明在系统设置中处理"
              >
                仅提示·不可一键清理
              </span>
              <span
                v-else-if="needsAdmin(item)"
                class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] font-medium text-amber-600"
                title="此操作需要管理员权限，普通权限下会清理失败"
              >
                需管理员
              </span>
              <span
                v-if="item.running"
                class="rounded-full bg-red-100 px-2 py-0.5 text-[10px] font-semibold text-red-600"
                title="相关进程正在运行，清理时会先强制终止"
              >
                ● 运行中
              </span>
              <span
                class="rounded-full border px-2 py-0.5 text-[10px] font-semibold"
                :class="riskStyle[item.riskLevel] ?? riskStyle.low"
              >
                {{ riskLabel[item.riskLevel] ?? "低风险" }}
              </span>
              <span
                class="rounded px-1.5 py-0.5 text-[10px] font-medium"
                :class="actionStyle[item.action] ?? 'bg-slate-100 text-slate-500'"
              >
                {{ actionLabel[item.action] ?? item.action }}
              </span>
            </div>
            <div class="mt-0.5 truncate font-mono text-xs text-slate-400">{{ item.path }}</div>
            <div class="mt-0.5 text-xs text-slate-500">
              {{ item.location }}<template v-if="item.detail"> · {{ item.detail }}</template>
            </div>
            <div v-if="item.impact" class="mt-1 text-xs text-amber-600">
              ⚠️ {{ item.impact }}
            </div>
          </div>
          <!-- 行内快捷操作：打开位置 / 单条清理 -->
          <div class="flex shrink-0 flex-col items-stretch gap-1.5 pl-1">
            <button
              v-if="canOpen(item)"
              class="rounded-md border border-slate-300 px-2.5 py-1 text-xs text-slate-600 transition-colors hover:border-blue-400 hover:bg-blue-50 hover:text-blue-700"
              title="在资源管理器中打开该目录 / 定位该文件"
              @click="openLocation(item)"
            >
              📂 打开位置
            </button>
            <button
              v-if="item.action !== 'hint'"
              class="rounded-md border border-red-200 px-2.5 py-1 text-xs text-red-600 transition-colors hover:bg-red-50"
              title="备份后隔离/禁用/修复该项（可在恢复记录中找回）"
              @click="requestItemClean(item)"
            >
              🗑️ 清理该项
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 单条清理确认弹窗 -->
    <AppModal
      :open="itemConfirmOpen"
      title="确认清理该项"
      subtitle="清理前会自动备份，可在「恢复记录」中一键找回"
      confirm-text="确认清理"
      tone="danger"
      :loading="itemDeleting"
      @confirm="confirmItemClean"
      @cancel="itemConfirmOpen = false; pendingItem = null"
    >
      <template v-if="pendingItem">
        <div class="rounded-xl bg-slate-50 p-3">
          <div class="text-xs text-slate-400">{{ pendingItem.location }}</div>
          <div class="mt-0.5 text-sm font-semibold text-slate-700">{{ pendingItem.name }}</div>
          <div class="mt-0.5 break-all font-mono text-xs text-slate-500">{{ pendingItem.path || pendingItem.value }}</div>
        </div>
        <p v-if="pendingItem.ruleType === 'process'" class="mt-3 rounded-lg bg-amber-50 p-2 text-xs text-amber-700">
          进程类项目将直接结束相关进程（无备份）；其自启动项需另行清理。
        </p>
        <p class="mt-3 text-xs text-slate-500">
          目录/文件/注册表项将先备份再隔离，恢复记录中可还原。
        </p>
      </template>
    </AppModal>
  </div>
</template>
