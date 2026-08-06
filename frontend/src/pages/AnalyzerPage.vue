<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { AnalyzerService } from "../../bindings/drivewise/backend/analyzer";
import type { DriveInfo, FileNode, ScanOptions } from "../../bindings/drivewise/backend/models";
import { formatBytes, formatCount } from "../utils/format";
import DirTreeRow from "../components/DirTreeRow.vue";
import AppModal from "../components/AppModal.vue";

type Engine = "auto" | "mft" | "walk";

const engineOptions: { v: Engine; t: string }[] = [
  { v: "auto", t: "自动" },
  { v: "mft", t: "⚡ MFT 极速" },
  { v: "walk", t: "精确遍历" },
];

const drives = ref<DriveInfo[]>([]);
const selectedDrive = ref("C:\\");
const engine = ref<Engine>("auto");
const scanning = ref(false);
const treeRoot = ref<FileNode | null>(null);
const scanTime = ref("");
const scanError = ref("");
const activeEngine = ref<"mft" | "walk">("walk");
const engineMsg = ref("");

/* ========== 删除确认弹窗 ========== */
const showDeleteConfirm = ref(false);
const pendingDelete = ref("");
const deleting = ref(false);
const deleteError = ref("");

/* ========== Toast 提示 ========== */
const toast = ref<{ type: "ok" | "err"; text: string } | null>(null);
let toastTimer: ReturnType<typeof setTimeout> | null = null;
function showToast(type: "ok" | "err", text: string) {
  toast.value = { type, text };
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = null), 4000);
}

const isRootLoaded = computed(() => !!treeRoot.value);

const engineBadge = computed(() => {
  if (!isRootLoaded.value) return null;
  return activeEngine.value === "mft"
    ? { text: "⚡ MFT 极速扫描", cls: "bg-violet-100 text-violet-700 border-violet-200" }
    : { text: "📁 目录遍历", cls: "bg-blue-100 text-blue-700 border-blue-200" };
});

onMounted(async () => {
  drives.value = (await AnalyzerService.GetDrives()) ?? [];
  if (drives.value.length) {
    selectedDrive.value = drives.value[0].name + "\\";
  }
  await loadRoot();
});

/** 扫描选中盘（组合引擎） */
async function loadRoot() {
  scanning.value = true;
  scanError.value = "";
  engineMsg.value = "";
  const t0 = performance.now();
  try {
    const opts: ScanOptions = { path: selectedDrive.value, maxDepth: 1, topN: 50, engine: engine.value };
    const res = await AnalyzerService.ScanDisk(opts);
    if (res.node) sortChildren(res.node);
    treeRoot.value = res.node;
    activeEngine.value = res.engine === "mft" ? "mft" : "walk";
    scanTime.value = ((performance.now() - t0) / 1000).toFixed(1);
    if (res.message) engineMsg.value = res.message;
  } catch (e) {
    scanError.value = `扫描失败：${String(e)}`;
    treeRoot.value = null;
  } finally {
    scanning.value = false;
  }
}

/** 按大小降序排列子级（保证每层从大到小） */
function sortChildren(node: FileNode) {
  if (!node.children) return;
  const sorted = (node.children as (FileNode | null)[]).filter(
    (c): c is FileNode => c !== null,
  );
  sorted.sort((a, b) => (b.size ?? 0) - (a.size ?? 0));
  node.children = sorted;
}

/** 懒加载节点子级：MFT 模式走缓存查询，遍历模式逐层扫 */
async function loadChildren(node: FileNode) {
  if (node.children && node.children.length > 0) return;
  let kids: (FileNode | null)[] | null = null;
  if (activeEngine.value === "mft") {
    kids = await AnalyzerService.GetDirChildren(node.path);
  } else {
    const opts: ScanOptions = { path: node.path, maxDepth: 1, topN: 50, engine: "walk" };
    const sub = await AnalyzerService.ScanDirectory(opts);
    if (sub) kids = sub.children ?? [];
  }
  if (kids) {
    const sorted = kids.filter((c): c is FileNode => c !== null);
    sorted.sort((a, b) => (b.size ?? 0) - (a.size ?? 0));
    node.children = sorted;
  }
}

/** 重新扫描当前选中盘 */
async function rescan() {
  treeRoot.value = null;
  await loadRoot();
}

/** 右键：打开目录 */
async function openInExplorer(path: string) {
  try {
    await AnalyzerService.OpenInExplorer(path);
  } catch (e) {
    showToast("err", `打开失败：${String(e)}`);
  }
}

/** 右键：移入回收站（弹窗确认） */
function requestDelete(path: string) {
  pendingDelete.value = path;
  deleteError.value = "";
  showDeleteConfirm.value = true;
}

/** 确认删除 */
async function confirmDelete() {
  deleting.value = true;
  deleteError.value = "";
  try {
    const freed = await AnalyzerService.DeletePath(pendingDelete.value);
    showDeleteConfirm.value = false;
    showToast("ok", `已移入回收站，释放约 ${formatBytes(freed)}，可在回收站中恢复`);
    subtractAncestors(pendingDelete.value, freed);
    refreshParent(pendingDelete.value);
  } catch (e) {
    deleteError.value = String(e);
  } finally {
    deleting.value = false;
  }
}

/** 删除成功后沿祖先链扣减大小（立即动态刷新，不等重新扫描） */
function subtractAncestors(path: string, size: number) {
  if (!treeRoot.value || size <= 0) return;
  const parents = new Map<FileNode, FileNode>();
  const queue: FileNode[] = [treeRoot.value];
  let target: FileNode | null = null;
  while (queue.length && !target) {
    const n = queue.shift()!;
    for (const c of n.children ?? []) {
      if (!c) continue;
      parents.set(c, n);
      if (c.path === path) {
        target = c;
        break;
      }
      queue.push(c);
    }
  }
  if (!target) return;
  for (let p = parents.get(target); p; p = parents.get(p)) {
    p.size = Math.max(0, (p.size ?? 0) - size);
  }
}

/** 删除后刷新被删节点的父级（重置 children 并重新加载） */
function refreshParent(path: string) {
  if (!treeRoot.value) return;
  const parent = findParent(treeRoot.value, path);
  const target = parent ?? treeRoot.value;
  if (target) {
    target.children = [];
    void loadChildren(target);
  }
}

/** 在树中查找节点的父节点（BFS） */
function findParent(root: FileNode, path: string): FileNode | null {
  const queue: FileNode[] = [root];
  while (queue.length) {
    const n = queue.shift()!;
    for (const c of n.children ?? []) {
      if (!c) continue;
      if (c.path === path) return n;
      queue.push(c);
    }
  }
  return null;
}

/** 统计树节点总数 */
function countNodes(node: FileNode | null): number {
  if (!node) return 0;
  let n = 1;
  for (const c of node.children ?? []) {
    if (c) n += countNodes(c);
  }
  return n;
}
</script>

<template>
  <div class="mx-auto max-w-6xl p-8">
    <!-- 页头 -->
    <header class="mb-6 flex items-center gap-4">
      <div class="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-gradient-to-br from-blue-500 to-indigo-600 shadow-lg shadow-blue-200">
        <svg viewBox="0 0 24 24" fill="none" stroke="white" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" class="h-6 w-6">
          <path d="M3 3v18h18M7 15l4-6 3 4 5-8" />
        </svg>
      </div>
      <div>
        <h1 class="text-2xl font-bold text-slate-800">磁盘分析</h1>
        <p class="mt-0.5 text-sm text-slate-500">
          树形目录展示空间占用，点击文件夹展开下级；右键可打开目录或移入回收站（可恢复）。悬停 ⓘ 了解目录用途。
        </p>
      </div>
    </header>

    <!-- 操作栏 -->
    <div class="mb-5 flex flex-wrap items-center justify-between gap-4 rounded-2xl border border-slate-200 bg-white p-4 shadow-sm">
      <div class="flex flex-wrap items-center gap-3 text-sm text-slate-600">
        <span>分析磁盘：</span>
        <select
          v-model="selectedDrive"
          class="rounded-lg border border-slate-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none"
          @change="rescan"
        >
          <option v-for="d in drives" :key="d.name" :value="d.name + '\\'">
            {{ d.name }}（{{ formatBytes(d.used) }} / {{ formatBytes(d.total) }}）
          </option>
        </select>

        <!-- 引擎选择 -->
        <div class="flex overflow-hidden rounded-lg border border-slate-300">
          <button
            v-for="e in engineOptions"
            :key="e.v"
            class="px-3 py-1.5 text-xs font-medium transition-colors"
            :class="engine === e.v ? 'bg-blue-600 text-white' : 'bg-white text-slate-600 hover:bg-slate-50'"
            :title="e.v === 'mft' ? '直接解析 NTFS 主文件表（需管理员），秒级扫描' : e.v === 'walk' ? '目录遍历，最精确但较慢' : '管理员+NTFS 时用 MFT，否则遍历'"
            @click="engine = e.v; rescan()"
          >
            {{ e.t }}
          </button>
        </div>

        <button
          class="rounded-lg bg-blue-600 px-4 py-1.5 text-sm font-medium text-white shadow-sm shadow-blue-200 transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="scanning"
          @click="rescan"
        >
          {{ scanning ? "扫描中..." : "重新扫描" }}
        </button>
      </div>
      <div v-if="treeRoot" class="flex items-center gap-3 text-sm text-slate-500">
        <span
          v-if="engineBadge"
          class="rounded-full border px-2.5 py-0.5 text-xs font-medium"
          :class="engineBadge.cls"
        >
          {{ engineBadge.text }}
        </span>
        <span>
          {{ selectedDrive }} 总占用
          <span class="font-semibold text-slate-700">{{ formatBytes(treeRoot.size) }}</span>
          <span class="mx-2 text-slate-300">|</span>
          耗时 <span class="font-semibold text-slate-700">{{ scanTime }}s</span>
        </span>
      </div>
    </div>

    <div v-if="engineMsg" class="mb-5 rounded-xl border border-amber-200 bg-amber-50 p-3 text-xs text-amber-700">
      ⚠️ {{ engineMsg }}
    </div>
    <div v-if="scanError" class="mb-5 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">
      {{ scanError }}
    </div>

    <!-- 树形目录 -->
    <div class="overflow-hidden rounded-2xl border border-slate-200 bg-white shadow-sm">
      <div
        v-if="scanning && !treeRoot"
        class="flex h-[480px] items-center justify-center text-sm text-slate-400"
      >
        正在分析 {{ selectedDrive }} ...
      </div>
      <div
        v-else-if="!treeRoot"
        class="flex h-[480px] flex-col items-center justify-center text-slate-400"
      >
        <span class="mb-3 text-4xl">📊</span>
        <p class="text-sm">选择磁盘并点击「重新扫描」</p>
      </div>
      <div v-else class="p-3">
        <!-- 根节点（右键打开/删除/刷新冒泡） -->
        <DirTreeRow
          :node="treeRoot"
          :depth="0"
          :on-load="loadChildren"
          @open="openInExplorer"
          @delete="requestDelete"
          @refresh="refreshParent"
        />
        <div class="mt-3 border-t border-slate-100 pt-2 text-center text-xs text-slate-400">
          共 {{ formatCount(countNodes(treeRoot)) }} 个条目 · 点击文件夹展开下级 · 右键可打开目录 / 移入回收站
          <template v-if="activeEngine === 'mft'">（MFT 模式：按文件记录统计，硬链接去重，极速）</template>
        </div>
      </div>
    </div>

    <!-- 删除确认弹窗 -->
    <AppModal
      :open="showDeleteConfirm"
      title="移入回收站"
      subtitle="文件不会永久删除，可从回收站恢复"
      confirm-text="确认移入回收站"
      tone="danger"
      :loading="deleting"
      @confirm="confirmDelete"
      @cancel="showDeleteConfirm = false"
    >
      <div class="rounded-xl bg-slate-50 p-3">
        <div class="text-xs text-slate-400">将删除以下路径：</div>
        <div class="mt-1 break-all font-mono text-sm font-medium text-slate-700">{{ pendingDelete }}</div>
      </div>
      <p v-if="deleteError" class="mt-3 rounded-lg bg-red-50 p-2 text-xs text-red-600">{{ deleteError }}</p>
      <p class="mt-3 text-xs text-slate-500">
        ⚠️ 系统关键目录（Windows / Program Files / Users / 磁盘根目录等）受保护，无法删除。
      </p>
    </AppModal>

    <!-- Toast -->
    <Transition name="toast">
      <div
        v-if="toast"
        class="fixed bottom-8 right-8 z-[90] flex items-center gap-2 rounded-xl px-4 py-3 text-sm font-medium text-white shadow-xl"
        :class="toast.type === 'ok' ? 'bg-emerald-500' : 'bg-red-500'"
      >
        {{ toast.text }}
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition: all 0.25s ease;
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(10px);
}
</style>
