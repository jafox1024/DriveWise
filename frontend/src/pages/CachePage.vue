<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { storeToRefs } from "pinia";
import { CacheService } from "../../bindings/drivewise/backend/cleaner";
import { SysService } from "../../bindings/drivewise/backend/sysinfo";
import { useCacheStore } from "../stores/cache";
import { useWinSxSStore } from "../stores/winsxs";
import { formatBytes, formatCount } from "../utils/format";
import { explainPath } from "../utils/pathExplain";
import AppModal from "../components/AppModal.vue";
import type {
  CacheCategory,
  CacheDetailItem,
  CacheDetailPath,
  CleanResult,
  OSUpgradeRemnant,
  OSCleanItemResult,
} from "../../bindings/drivewise/backend/models";

const store = useCacheStore();
const { categories, scanning, cleaning, lastResults, lastScanTime } =
  storeToRefs(store);

/* WinSxS 状态提升到全局 store：切换页面不丢进度 */
const winsxsStore = useWinSxSStore();
const {
  status: winsxsStatus,
  error: winsxsError,
  apiBusy: winsxsApiBusy,
  running: winsxsRunning,
  done: winsxsDone,
} = storeToRefs(winsxsStore);

const expanded = ref<Set<string>>(new Set());

/* ========== 详情视图状态 ========== */
const detailView = ref(false);
const detailCategory = ref("");
const details = ref<CacheDetailPath[]>([]);
const detailLoading = ref(false);
const detailCleanIds = ref<Set<string>>(new Set());
const detailCleaning = ref(false);
const detailTotal = ref(0);

/* WinSxS 确认弹窗（页面局部 UI 状态） */
const showWinSxSConfirm = ref(false);

/* 系统级高级功能区默认折叠，降低小白认知负担 */
const showAdvanced = ref(false);

const allSelected = computed(() => {
  // 数据保护分类（聊天/办公数据）不可勾选，不计入全选
  const list = categories.value.filter((c) => !c.dataOnly);
  return list.length > 0 && list.every((c) => c.selected);
});

/** 当前详情视图对应的分类对象（用于识别数据保护分类） */
const detailCat = computed(() =>
  categories.value.find((c) => c.name === detailCategory.value),
);

const totalFreed = computed(() =>
  lastResults.value.reduce((s, r) => s + (r.freedBytes || 0), 0),
);

const hasErrors = computed(() =>
  lastResults.value.some((r) => (r.errors ?? []).length > 0),
);

const detailSelectedSize = computed(() => {
  let size = 0;
  for (const d of details.value) {
    for (const it of d.items ?? []) {
      if (detailCleanIds.value.has(it.path)) size += it.size;
    }
  }
  return size;
});

/* ========== WinSxS 派生状态 ========== */
/* winsxsRunning / winsxsDone 由 store 提供（storeToRefs 解构），此处只保留耗时文本 */
const winsxsElapsedText = computed(() => {
  const s = winsxsStatus.value?.elapsedSec ?? 0;
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;
});

function togglePath(name: string) {
  const next = new Set(expanded.value);
  if (next.has(name)) next.delete(name);
  else next.add(name);
  expanded.value = next;
}

/** 安全获取分类清理失败项 */
function catErrors(r: CleanResult): string[] {
  return r.errors ?? [];
}

/** 分类说明（后端 desc 为空时按路径名智能匹配常见目录解释） */
function catDesc(cat: CacheCategory): string {
  if (cat.desc) return cat.desc;
  for (const p of cat.paths ?? []) {
    const exp = explainPath(p);
    if (exp) return exp;
  }
  return "";
}

/* ========== 详情视图 ========== */
async function openDetail(cat: CacheCategory) {
  detailCategory.value = cat.name;
  detailView.value = true;
  detailCleanIds.value = new Set();
  await refreshDetails();
}

async function refreshDetails() {
  detailLoading.value = true;
  detailTotal.value = 0;
  try {
    const result = await CacheService.ScanCacheDetails(detailCategory.value);
    details.value = result ?? [];
    detailTotal.value = details.value.reduce((s, d) => s + (d.size || 0), 0);
  } finally {
    detailLoading.value = false;
  }
}

function closeDetail() {
  detailView.value = false;
  details.value = [];
  store.scan();
}

/** 按路径查找详情条目（用于判断是否为空目录） */
function findDetailItem(path: string): CacheDetailItem | undefined {
  for (const d of details.value) {
    for (const it of d.items ?? []) {
      if (it.path === path) return it;
    }
  }
  return undefined;
}

/** 可勾选清理的条目：普通分类全部；数据保护分类仅空目录（无文件残留） */
function selectableItems(d: CacheDetailPath): CacheDetailItem[] {
  if (detailCat.value?.dataOnly) {
    return (d.items ?? []).filter((it) => it.isDir && it.empty);
  }
  return d.items ?? [];
}

/** 数据保护分类下可清理（空目录）数量 */
function selectableCount(d: CacheDetailPath): number {
  return selectableItems(d).length;
}

function toggleDetailItem(path: string) {
  const it = findDetailItem(path);
  // 数据保护分类：仅空目录（整棵子树无文件）可勾选删除，其余禁止
  if (detailCat.value?.dataOnly && !(it?.isDir && it.empty)) return;
  const next = new Set(detailCleanIds.value);
  if (next.has(path)) next.delete(path);
  else next.add(path);
  detailCleanIds.value = next;
}

function selectAllInPath(d: CacheDetailPath, checked: boolean) {
  const next = new Set(detailCleanIds.value);
  for (const it of selectableItems(d)) {
    if (checked) next.add(it.path);
    else next.delete(it.path);
  }
  detailCleanIds.value = next;
}

async function cleanDetailItems() {
  if (!detailCleanIds.value.size) return;
  detailCleaning.value = true;
  try {
    const res = await CacheService.CleanCacheItems(
      detailCategory.value,
      [...detailCleanIds.value],
    );
    lastResults.value = [res];
    await refreshDetails();
  } finally {
    detailCleaning.value = false;
  }
}

/* ========== WinSxS 后台清理 ========== */
/** 弹出确认框 */
function requestWinSxSClean() {
  winsxsError.value = "";
  showWinSxSConfirm.value = true;
}

/** 确认后启动后台清理（状态与轮询由全局 store 管理） */
function confirmWinSxSClean() {
  showWinSxSConfirm.value = false;
  void winsxsStore.startClean();
}

/** 需要管理员权限时：以管理员身份重启本程序（UAC 提权），重启后由用户重新触发清理 */
async function relaunchAsAdmin() {
  try {
    await SysService.RestartAsAdmin();
  } catch (e) {
    winsxsError.value = "提权失败：" + ((e as Error)?.message ?? String(e));
  }
}

/* ========== WinSxS 激进清理（/ResetBase，不可逆） ========== */
const showWinSxSResetConfirm = ref(false);

function requestWinSxSReset() {
  winsxsError.value = "";
  showWinSxSResetConfirm.value = true;
}

function confirmWinSxSReset() {
  showWinSxSResetConfirm.value = false;
  void winsxsStore.startClean(true);
}

/* ========== 系统升级残留（$WINDOWS.~BT / Windows.old 等） ========== */
const osRemnants = ref<OSUpgradeRemnant[]>([]);
const osScanLoading = ref(false);
const osSelected = ref<Set<string>>(new Set());
const osCleaning = ref(false);
const osResults = ref<OSCleanItemResult[]>([]);
const showOSCleanConfirm = ref(false);
const osScanError = ref("");

const osTotalSize = computed(() =>
  osRemnants.value
    .filter((r) => osSelected.value.has(r.path))
    .reduce((s, r) => s + (r.size || 0), 0),
);

function osAllSelected(): boolean {
  return osRemnants.value.length > 0 && osRemnants.value.every((r) => osSelected.value.has(r.path));
}

function toggleOS(path: string, v: boolean) {
  if (v) osSelected.value.add(path);
  else osSelected.value.delete(path);
  osSelected.value = new Set(osSelected.value);
}

function toggleOSAll(v: boolean) {
  const set = new Set<string>();
  if (v) for (const r of osRemnants.value) set.add(r.path);
  osSelected.value = set;
}

async function scanOSRemnants() {
  osScanLoading.value = true;
  osScanError.value = "";
  try {
    osRemnants.value = (await CacheService.ScanOSUpgradeRemnants()) ?? [];
    // 默认全部选中
    osSelected.value = new Set(osRemnants.value.map((r) => r.path));
    osResults.value = [];
  } catch (e) {
    osScanError.value = (e as Error)?.message ?? String(e);
  } finally {
    osScanLoading.value = false;
  }
}

function requestCleanOS() {
  showOSCleanConfirm.value = true;
}

async function confirmCleanOS() {
  showOSCleanConfirm.value = false;
  if (osSelected.value.size === 0) return;
  osCleaning.value = true;
  osResults.value = [];
  const paths = Array.from(osSelected.value);
  try {
    const results = (await CacheService.CleanOSUpgradeRemnants(paths)) ?? [];
    osResults.value = results;
    // 清理后清除成功项的选中，重新扫描刷新占用
    for (const r of results) {
      if (r.success) osSelected.value.delete(r.path);
      osSelected.value = new Set(osSelected.value);
    }
    // 重新扫描看还有哪些残留
    await scanOSRemnantsPreserve();
  } catch (e) {
    osScanError.value = (e as Error)?.message ?? String(e);
  } finally {
    osCleaning.value = false;
  }
}

/** 清理后刷新：只保留仍存在的目录，选中项按最新结果重新标记 */
async function scanOSRemnantsPreserve() {
  const keep = new Set(Array.from(osSelected.value));
  osScanLoading.value = true;
  try {
    const list = (await CacheService.ScanOSUpgradeRemnants()) ?? [];
    osRemnants.value = list;
    const set = new Set<string>();
    for (const r of list) {
      if (keep.has(r.path)) set.add(r.path);
    }
    osSelected.value = set;
  } finally {
    osScanLoading.value = false;
  }
}

/* 页面挂载时恢复 WinSxS 进度：清理若仍在后台运行则续上轮询，避免切换页面后进度丢失 */
onMounted(() => {
  void winsxsStore.ensurePolling();
  void scanOSRemnants();
});
</script>

<template>
  <div class="mx-auto max-w-5xl p-8">
    <!-- ===== 详情视图 ===== -->
    <template v-if="detailView">
      <header class="mb-6 flex items-center gap-4">
        <button
          class="rounded-lg border border-slate-300 px-3 py-1.5 text-sm text-slate-600 transition-colors hover:bg-slate-100"
          @click="closeDetail"
        >
          ← 返回
        </button>
        <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-blue-500 to-indigo-600 shadow-md shadow-blue-200">
          <svg viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="h-5 w-5">
            <path d="M3 6h18M5 6v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V6M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
          </svg>
        </div>
        <div>
          <h1 class="text-2xl font-bold text-slate-800">{{ detailCategory }} · 详情</h1>
          <p v-if="detailCat?.dataOnly" class="mt-1 text-sm text-rose-600">
            该分类为聊天记录/办公文档等个人数据。<b>仅可删除空目录</b>（无任何文件的残留目录结构），有内容的条目一律禁止清理
          </p>
          <p v-else class="mt-1 text-sm text-slate-500">
            勾选需要删除的具体条目，总占用 {{ formatBytes(detailTotal) }}；删除后可在回收站找回
          </p>
        </div>
      </header>

      <!-- 数据保护分类：迁移提示条 -->
      <div
        v-if="detailCat?.dataOnly"
        class="mb-5 flex items-start gap-3 rounded-2xl border border-rose-200 bg-rose-50 p-4 text-sm text-rose-700"
      >
        <span class="mt-0.5 shrink-0 text-lg leading-none">🛡️</span>
        <div class="min-w-0">
          <div class="font-medium">非空内容不可清理</div>
          <p class="mt-1 text-xs leading-relaxed text-rose-600">
            {{ detailCat?.migrateHint || "删除这些数据将导致聊天记录/文档永久丢失。" }}
          </p>
        </div>
      </div>

      <!-- 清理操作条 -->
      <div class="sticky top-0 z-10 mb-5 flex items-center justify-between rounded-2xl border border-slate-200 bg-white/95 p-4 shadow-sm backdrop-blur">
        <span class="text-sm text-slate-600">
          已选 <span class="font-bold text-blue-600">{{ detailCleanIds.size }}</span> 项
          <template v-if="!detailCat?.dataOnly">（{{ formatBytes(detailSelectedSize) }}）</template>
          <template v-else>（空目录）</template>
        </span>
        <button
          class="rounded-lg bg-red-600 px-5 py-2 text-sm font-medium text-white shadow-md shadow-red-200 transition-colors hover:bg-red-700 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="detailCleaning || detailCleanIds.size === 0"
          @click="cleanDetailItems"
        >
          {{ detailCleaning
            ? "清理中..."
            : detailCat?.dataOnly ? "删除选中空目录" : "清理选中条目" }}
        </button>
      </div>

      <div v-if="detailLoading" class="py-16 text-center text-sm text-slate-400">
        正在读取缓存详情...
      </div>

      <div v-else class="space-y-4">
        <div
          v-for="d in details"
          :key="d.path"
          class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm"
        >
          <div class="flex items-center justify-between gap-3 border-b border-slate-100 bg-slate-50/60 px-4 py-2.5">
            <div class="min-w-0 flex-1">
              <div class="truncate font-mono text-xs text-slate-600">{{ d.path }}</div>
              <div class="text-xs text-slate-400">
                {{ formatBytes(d.size) }} · {{ formatCount(d.fileCount) }} 个文件
                <span
                  v-if="explainPath(d.path)"
                  class="ml-1 cursor-help rounded bg-blue-50 px-1.5 py-0.5 text-[10px] text-blue-500"
                  :title="explainPath(d.path) ?? undefined"
                >{{ explainPath(d.path) }}</span>
              </div>
            </div>
            <label v-if="selectableCount(d) > 0" class="flex shrink-0 cursor-pointer items-center gap-1.5 text-xs text-slate-500">
              <input
                type="checkbox"
                class="h-3.5 w-3.5 rounded border-slate-300 text-blue-600"
                :checked="selectableItems(d).every((it) => detailCleanIds.has(it.path))"
                @change="(e) => selectAllInPath(d, (e.target as HTMLInputElement).checked)"
              />
              {{ detailCat?.dataOnly ? "全选空目录" : "全选" }}
            </label>
          </div>

          <div v-if="(d.items ?? []).length === 0" class="px-4 py-3 text-xs text-slate-400">
            无条目或目录为空
          </div>
          <ul v-else class="max-h-80 divide-y divide-slate-50 overflow-y-auto">
            <li
              v-for="it in d.items"
              :key="it.path"
              class="flex items-center gap-3 px-4 py-2 transition-colors hover:bg-slate-50/60"
            >
              <input
                type="checkbox"
                class="h-4 w-4 shrink-0 rounded border-slate-300 text-blue-600 focus:ring-blue-500"
                :disabled="detailCat?.dataOnly ? !(it.isDir && it.empty) : false"
                :checked="detailCleanIds.has(it.path)"
                @change="toggleDetailItem(it.path)"
              />
              <span class="w-4 shrink-0 text-slate-300">{{ it.isDir ? "📁" : "📄" }}</span>
              <span class="min-w-0 flex-1 truncate text-sm text-slate-700" :title="it.path">
                {{ it.name }}
              </span>
              <!-- 数据保护分类：标注可删空目录 / 禁删数据 -->
              <span
                v-if="detailCat?.dataOnly && it.isDir && it.empty"
                class="shrink-0 rounded bg-emerald-100 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600"
              >空目录·可删</span>
              <span
                v-else-if="detailCat?.dataOnly"
                class="shrink-0 rounded bg-rose-100 px-1.5 py-0.5 text-[10px] font-medium text-rose-500"
              >数据·禁删</span>
              <span
                v-if="explainPath(it.name)"
                class="shrink-0 cursor-help text-slate-300 hover:text-blue-500"
                :title="explainPath(it.name) ?? undefined"
              >ⓘ</span>
              <span class="shrink-0 text-xs text-slate-400">{{ it.modTime }}</span>
              <span
                class="w-24 shrink-0 text-right text-sm font-medium tabular-nums"
                :class="it.size > 100 * 1024 * 1024 ? 'text-red-600' : 'text-slate-600'"
              >
                {{ formatBytes(it.size) }}
              </span>
            </li>
          </ul>
        </div>
      </div>
    </template>

    <!-- ===== 列表视图 ===== -->
    <template v-else>
      <!-- 页头 -->
      <header class="mb-6 flex items-center gap-4">
        <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-gradient-to-br from-cyan-500 to-blue-600 shadow-lg shadow-cyan-200">
          <svg viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" class="h-6 w-6">
            <path d="M3 6h18M5 6v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V6M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M9 11v6M15 11v6" />
          </svg>
        </div>
        <div>
          <h1 class="text-2xl font-bold text-slate-800">缓存清理</h1>
          <p class="mt-0.5 text-sm text-slate-500">
            帮你清掉电脑里没用的临时文件和缓存，释放 C 盘空间。安全项已默认勾选，直接点「清理选中」即可。
          </p>
        </div>
      </header>

      <!-- 关键数字（只留两个最关键，降低密度） -->
      <div class="mb-5 grid grid-cols-2 gap-4">
        <div class="rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
          <div class="text-xs text-slate-400">可释放空间</div>
          <div class="mt-1 text-2xl font-bold text-slate-800">{{ formatBytes(store.cleanableSize) }}</div>
        </div>
        <div class="rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
          <div class="text-xs text-slate-400">
            已选 {{ categories.filter((c) => c.selected && !c.dataOnly).length }} 项
          </div>
          <div
            class="mt-1 text-2xl font-bold"
            :class="store.selectedSize > 0 ? 'text-blue-600' : 'text-slate-800'"
          >
            {{ formatBytes(store.selectedSize) }}
          </div>
        </div>
      </div>

      <!-- 高级功能：系统级深度清理（默认折叠，减少信息量） -->
      <div class="mb-5 overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
        <button
          class="flex w-full items-center justify-between gap-3 px-5 py-4 text-left transition-colors hover:bg-slate-50"
          @click="showAdvanced = !showAdvanced"
        >
          <div class="min-w-0">
            <div class="text-sm font-semibold text-slate-700">高级功能：系统级深度清理</div>
            <div class="mt-0.5 text-xs text-slate-400">
              清理 Windows 更新组件、系统升级残留等，需要管理员权限。日常使用无需理会。
            </div>
          </div>
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            class="h-4 w-4 shrink-0 text-slate-400 transition-transform"
            :class="{ 'rotate-90': showAdvanced }"
          >
            <path d="m9 18 6-6-6-6" />
          </svg>
        </button>

        <div v-if="showAdvanced" class="space-y-4 border-t border-slate-100 bg-slate-50/40 p-4">
          <!-- WinSxS 组件存储 -->
          <div class="overflow-hidden rounded-2xl border border-violet-200 bg-gradient-to-br from-violet-50 via-white to-white p-5 shadow-sm">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-sm font-semibold text-violet-800">Windows 组件存储 (WinSxS)</h2>
              <span class="rounded-full bg-violet-100 px-2 py-0.5 text-[10px] font-medium text-violet-600">系统级 · 需管理员权限</span>
              <span
                v-if="winsxsRunning"
                class="flex items-center gap-1 rounded-full bg-violet-600 px-2 py-0.5 text-[10px] font-medium text-white"
              >
                <span class="h-1.5 w-1.5 animate-ping rounded-full bg-white"></span>
                后台清理中
              </span>
            </div>
            <p class="mt-1 text-xs leading-relaxed text-slate-500">
              清理 Windows 更新与系统组件的旧版本残留，通常可释放数 GB 空间。清理在<strong class="text-violet-700">后台执行</strong>，不阻塞其他功能。
            </p>

            <!-- 空闲态 -->
            <div v-if="!winsxsStatus || (!winsxsRunning && !winsxsDone)" class="mt-2 text-xs text-slate-500">
              <span class="cursor-help text-slate-300" title="组件存储是系统更新组件的备份池，由系统自动管理，DISM 会安全回收其中不再使用的旧版本">ⓘ</span>
              尚未执行过清理。点右侧按钮开始。
            </div>

            <!-- 运行中 -->
            <div v-if="winsxsRunning" class="mt-2 flex items-center gap-2 text-xs text-violet-700">
              <span class="h-3 w-3 animate-spin rounded-full border-2 border-violet-300 border-t-violet-600"></span>
              正在后台清理，已进行 <b>{{ winsxsElapsedText }}</b>，预计 10–40 分钟，期间可正常使用其他功能
            </div>

            <!-- 完成态 -->
            <div v-if="winsxsDone && winsxsStatus" class="mt-2 text-xs">
              <div
                class="inline-flex items-center gap-1.5 rounded-lg px-2.5 py-1 font-medium"
                :class="winsxsStatus.success ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-600'"
              >
                {{ winsxsStatus.success ? "✅" : "⚠️" }} {{ winsxsStatus.message }}
              </div>

              <!-- 权限不足时提供一键提权入口 -->
              <button
                v-if="!winsxsStatus.success && String(winsxsStatus.message).includes('管理员')"
                class="mt-2 inline-flex items-center gap-1.5 rounded-lg bg-amber-500 px-3 py-1.5 text-xs font-semibold text-white shadow-sm transition-colors hover:bg-amber-600"
                @click="relaunchAsAdmin"
              >
                以管理员身份重启 DriveWise 后立即重试
              </button>
            </div>

            <div v-if="winsxsError" class="mt-2 text-xs text-red-500">{{ winsxsError }}</div>
            <pre
              v-if="winsxsDone && winsxsStatus?.output"
              class="mt-2 max-h-28 overflow-auto whitespace-pre-wrap rounded-lg bg-white/70 p-2 font-mono text-[10px] leading-4 text-slate-500"
            >{{ winsxsStatus.output }}</pre>
          </div>
          <div class="flex shrink-0 flex-col gap-2">
            <button
              class="rounded-lg bg-violet-600 px-5 py-2 text-sm font-medium text-white shadow-md shadow-violet-200 transition-colors hover:bg-violet-700 disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="winsxsRunning || winsxsApiBusy"
              @click="requestWinSxSClean"
            >
              <template v-if="winsxsRunning">后台清理中...</template>
              <template v-else-if="winsxsDone">{{ winsxsStatus?.success ? "再次清理" : "重试清理" }}</template>
              <template v-else>开始清理</template>
            </button>
            <button
              class="rounded-lg border border-amber-300 bg-amber-50 px-5 py-2 text-sm font-medium text-amber-800 shadow-sm transition-colors hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="winsxsRunning || winsxsApiBusy"
              @click="requestWinSxSReset"
            >
              激进清理（不可逆）
            </button>
          </div>
        </div>
      </div>

      <!-- 系统升级残留 -->
      <div
        class="overflow-hidden rounded-2xl border border-sky-200 bg-gradient-to-br from-sky-50 via-white to-white p-5 shadow-sm"
      >
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <h2 class="text-sm font-semibold text-sky-800">Windows 系统升级残留</h2>
              <span class="rounded-full bg-sky-100 px-2 py-0.5 text-[10px] font-medium text-sky-600"
                >系统级 · 需管理员</span
              >
              <span
                v-if="osScanLoading"
                class="flex items-center gap-1 rounded-full bg-sky-600 px-2 py-0.5 text-[10px] font-medium text-white"
              >
                <span class="h-1.5 w-1.5 animate-ping rounded-full bg-white"></span> 扫描中
              </span>
            </div>
            <p class="mt-1 text-xs leading-relaxed text-slate-500">
              大版本升级后 Windows 会保留旧系统目录（<code>$WINDOWS.~BT</code>、
              <code>Windows.old</code> 等），常见占用 2–15 GB，由系统高权限持有，清理前会自动夺回所有权。
            </p>

            <!-- 未发现 -->
            <div v-if="!osScanLoading && osRemnants.length === 0" class="mt-2 text-xs text-slate-500">
              ✅ 未发现系统升级残留（近期未做大版本升级，或已清理）。
            </div>

            <!-- 列表 -->
            <div v-if="osRemnants.length > 0" class="mt-3 space-y-2">
              <label
                class="flex cursor-pointer items-center justify-between gap-2 rounded-lg bg-white/70 px-3 py-2 text-xs text-slate-600"
              >
                <span>全选（当前可回收 <b>{{ formatBytes(osTotalSize) }}</b>）</span>
                <input
                  type="checkbox"
                  class="h-4 w-4 rounded border-slate-300 text-sky-600 focus:ring-sky-500"
                  :checked="osAllSelected()"
                  @change="(e) => toggleOSAll((e.target as HTMLInputElement).checked)"
                />
              </label>
              <div
                v-for="r in osRemnants"
                :key="r.path"
                class="flex items-center justify-between gap-3 rounded-lg border border-slate-100 bg-white/60 px-3 py-2"
              >
                <label class="flex min-w-0 flex-1 cursor-pointer items-center gap-2 text-xs">
                  <input
                    type="checkbox"
                    class="h-4 w-4 shrink-0 rounded border-slate-300 text-sky-600 focus:ring-sky-500"
                    :checked="osSelected.has(r.path)"
                    @change="(e) => toggleOS(r.path, (e.target as HTMLInputElement).checked)"
                  />
                  <span class="shrink-0 text-slate-700">{{ r.name }}</span>
                  <span class="truncate font-mono text-[10px] text-slate-400">{{ r.path }}</span>
                </label>
                <span class="shrink-0 text-xs font-medium text-sky-700">{{ formatBytes(r.size) }}</span>
              </div>
            </div>

            <!-- 清理结果 -->
            <div v-if="osResults.length > 0" class="mt-3 space-y-1.5 text-xs">
              <div
                v-for="res in osResults"
                :key="res.path"
                class="flex items-center gap-2 rounded-lg px-2.5 py-1.5"
                :class="res.success ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-600'"
              >
                {{ res.success ? "✅" : "⚠️" }}
                {{
                  res.success
                    ? `${res.name}：已清理，释放 ${formatBytes(res.freedBytes)}`
                    : `${res.name}：${res.error}`
                }}
              </div>
            </div>

            <div v-if="osScanError" class="mt-2 text-xs text-red-500">{{ osScanError }}</div>
          </div>

          <div class="flex shrink-0 flex-col items-stretch gap-2">
            <button
              class="rounded-lg bg-sky-600 px-5 py-2 text-sm font-medium text-white shadow-md shadow-sky-200 transition-colors hover:bg-sky-700 disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="osScanLoading || osCleaning || osSelected.size === 0"
              @click="requestCleanOS"
            >
              <template v-if="osCleaning">清理中...</template>
              <template v-else>清理所选（{{ osSelected.size }}）</template>
            </button>
            <button
              class="rounded-lg border border-slate-200 bg-white px-5 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 disabled:opacity-50"
              :disabled="osScanLoading || osCleaning"
              @click="scanOSRemnants"
            >
              重新扫描
            </button>
          </div>
        </div>
      </div>
        </div>
      </div>

      <!-- 操作栏 -->
      <div class="mb-5 flex items-center justify-between rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
        <div class="flex flex-col gap-1">
          <label class="flex cursor-pointer items-center gap-2 text-sm text-slate-600">
            <input
              type="checkbox"
              class="h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500"
              :checked="allSelected"
              :indeterminate="!allSelected && categories.some((c) => c.selected)"
              @change="(e) => store.toggleAll((e.target as HTMLInputElement).checked)"
            />
            全选可清理项
          </label>
          <span class="pl-6 text-[11px] text-slate-400">上次扫描：{{ lastScanTime || "尚未扫描" }}</span>
        </div>
        <div class="flex items-center gap-3">
          <button
            class="rounded-lg border border-slate-300 px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-50 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="scanning || cleaning"
            @click="store.scan"
          >
            {{ scanning ? "扫描中..." : "重新扫描" }}
          </button>
          <button
            class="rounded-lg bg-blue-600 px-5 py-2 text-sm font-medium text-white shadow-md shadow-blue-200 transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="scanning || cleaning || store.selectedSize <= 0"
            @click="store.clean"
          >
            {{ cleaning ? "清理中..." : `清理选中 (${formatBytes(store.selectedSize)})` }}
          </button>
        </div>
      </div>

      <!-- 清理结果提示 -->
      <div
        v-if="lastResults.length > 0"
        class="mb-5 rounded-2xl border p-4 text-sm shadow-sm"
        :class="hasErrors ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-emerald-200 bg-emerald-50 text-emerald-700'"
      >
        <div class="flex items-center gap-2 font-medium">
          <span class="inline-block h-2 w-2 rounded-full" :class="hasErrors ? 'bg-amber-500' : 'bg-emerald-500'"></span>
          {{ hasErrors ? "清理完成，部分文件被占用未能删除" : "清理完成" }}
          — 本次释放 <span class="font-bold">{{ formatBytes(totalFreed) }}</span>
        </div>
        <ul v-if="hasErrors" class="mt-2 space-y-0.5 text-xs text-amber-600">
          <li v-for="(r, i) in lastResults" :key="i">
            <template v-if="catErrors(r).length">
              {{ r.category }}：{{ catErrors(r).slice(0, 2).join("；") }}
              <span v-if="catErrors(r).length > 2">等 {{ catErrors(r).length }} 项</span>
            </template>
          </li>
        </ul>
      </div>

      <!-- 分类列表 -->
      <div class="space-y-3">
        <div
          v-if="categories.length === 0 && !scanning"
          class="flex flex-col items-center justify-center rounded-2xl border-2 border-dashed border-slate-200 bg-white py-16 text-slate-400"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="mb-3 h-10 w-10">
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.3-4.3" />
          </svg>
          <p class="text-sm">点击「重新扫描」开始分析缓存占用</p>
        </div>

        <div
          v-for="cat in categories"
          :key="cat.name"
          class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm transition-all hover:shadow-md"
          :class="{ 'opacity-60': !cat.exists }"
        >
          <div class="flex items-center gap-3 px-4 py-3.5">
            <input
              type="checkbox"
              v-model="cat.selected"
              :disabled="!cat.exists || cat.dataOnly"
              class="h-4 w-4 rounded border-slate-300 text-blue-600 focus:ring-blue-500"
            />
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="text-sm font-semibold text-slate-700">{{ cat.name }}</span>
                <span v-if="cat.dataOnly" class="rounded bg-rose-100 px-1.5 py-0.5 text-[10px] font-medium text-rose-600" title="聊天记录/办公文档等个人数据，禁止清理，仅提示迁移">数据保护·不可清理</span>
                <span v-if="cat.admin" class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] text-amber-600" title="该系统级目录需要管理员权限，普通权限下可能清理失败">需管理员</span>
                <span v-if="!cat.exists" class="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] text-slate-400">未检测到路径</span>
              </div>
              <!-- 白话说明 -->
              <div v-if="catDesc(cat)" class="mt-0.5 truncate text-xs text-slate-400">
                {{ catDesc(cat) }}
              </div>
              <!-- 数据保护分类：迁移建议（不截断，完整展示） -->
              <div
                v-if="cat.dataOnly && cat.migrateHint"
                class="mt-1 flex items-start gap-1.5 rounded-lg bg-rose-50 px-2 py-1.5 text-[11px] leading-relaxed text-rose-600"
              >
                <span class="shrink-0">🛡️</span>
                <span>{{ cat.migrateHint }}</span>
              </div>
            </div>
            <div class="flex items-center gap-4 text-sm">
              <span class="text-xs text-slate-400">{{ formatCount(cat.fileCount) }} 项</span>
              <span
                class="min-w-20 rounded-lg px-2.5 py-1 text-center font-semibold tabular-nums"
                :class="cat.size > 1024 * 1024 * 1024 ? 'bg-red-50 text-red-600' : cat.size > 100 * 1024 * 1024 ? 'bg-amber-50 text-amber-600' : 'bg-slate-100 text-slate-600'"
              >
                {{ formatBytes(cat.size) }}
              </span>
              <button
                class="rounded-md border border-slate-200 px-2.5 py-1 text-xs text-slate-500 transition-colors hover:border-blue-300 hover:text-blue-600"
                :disabled="!cat.exists || cat.size <= 0"
                @click="openDetail(cat)"
              >
                详情
              </button>
              <svg
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
                stroke-linejoin="round"
                class="h-4 w-4 cursor-pointer text-slate-300 transition-transform hover:text-slate-500"
                :class="{ 'rotate-90': expanded.has(cat.name) }"
                @click="togglePath(cat.name)"
              >
                <path d="m9 18 6-6-6-6" />
              </svg>
            </div>
          </div>
          <!-- 展开的路径详情 -->
          <div v-if="expanded.has(cat.name)" class="border-t border-slate-100 bg-slate-50/50 px-6 py-3">
            <ul class="space-y-1">
              <li v-for="p in cat.paths ?? []" :key="p" class="flex items-center gap-2 font-mono text-xs text-slate-500">
                <span class="text-slate-300">└</span>
                <span class="truncate">{{ p }}</span>
                <span
                  v-if="explainPath(p)"
                  class="cursor-help shrink-0 text-[10px] text-blue-400"
                  :title="explainPath(p) ?? undefined"
                >{{ explainPath(p) }}</span>
              </li>
            </ul>
          </div>
        </div>
      </div>
    </template>

    <!-- WinSxS 清理确认弹窗 -->
    <AppModal
      :open="showWinSxSConfirm"
      title="开始清理 Windows 组件存储？"
      subtitle="清理将在后台执行，耗时可能达 10–40 分钟，期间可正常使用其他功能"
      confirm-text="开始后台清理"
      tone="violet"
      @confirm="confirmWinSxSClean"
      @cancel="showWinSxSConfirm = false"
    >
      <ul class="space-y-2 text-xs text-slate-600">
        <li class="flex gap-2"><span>🔐</span> 需要以管理员身份运行，否则会失败并给出提示</li>
        <li class="flex gap-2"><span>🧹</span> 通过系统 DISM 工具安全回收组件存储中不再使用的旧版本</li>
        <li class="flex gap-2"><span>⏱️</span> 后台执行，不会卡住界面，可随时切到其他页面</li>
        <li class="flex gap-2"><span>⚠️</span> 清理期间请勿强制关闭本程序，以免中断</li>
      </ul>
    </AppModal>

    <!-- WinSxS 激进清理确认弹窗 -->
    <AppModal
      :open="showWinSxSResetConfirm"
      title="开始激进清理 WinSxS？（不可逆）"
      subtitle="在标准清理基础上额外删除所有历史版本组件，通常可多释放 1–20 GB"
      confirm-text="我了解，开始激进清理"
      tone="danger"
      @confirm="confirmWinSxSReset"
      @cancel="showWinSxSResetConfirm = false"
    >
      <ul class="space-y-2 text-xs text-slate-600">
        <li class="flex gap-2"><span>🔒</span> <b>不可逆</b>：Windows 组件回滚 / 还原此安装将失效</li>
        <li class="flex gap-2"><span>🔐</span> 需要以管理员身份运行，否则失败并提示提权</li>
        <li class="flex gap-2"><span>🧹</span> 命令：DISM /Online /Cleanup-Image /StartComponentCleanup /ResetBase</li>
        <li class="flex gap-2"><span>⏱️</span> 后台执行，耗时可能 15–50 分钟，勿强制关闭</li>
      </ul>
    </AppModal>

    <!-- 系统升级残留清理确认弹窗 -->
    <AppModal
      :open="showOSCleanConfirm"
      title="清理系统升级残留？"
      :subtitle="`将删除 ${osSelected.size} 个目录，预计释放 ${formatBytes(osTotalSize)}`"
      confirm-text="确认清理"
      tone="danger"
      @confirm="confirmCleanOS"
      @cancel="showOSCleanConfirm = false"
    >
      <ul class="space-y-2 text-xs text-slate-600">
        <li class="flex gap-2"><span>🗑️</span> 目标：<code>$WINDOWS.~BT</code>、<code>$WINDOWS.~WS</code>、<code>Windows.old</code> 等</li>
        <li class="flex gap-2"><span>🔐</span> 属系统高权限持有，需以管理员身份运行</li>
        <li class="flex gap-2"><span>🔁</span> 会自动执行 takeown / icacls 夺回所有权后删除</li>
        <li class="flex gap-2"><span>🚫</span> 删除前请确认未安装到一半的升级流程，否则可能影响升级回滚</li>
      </ul>
    </AppModal>
  </div>
</template>
