<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from "vue";
import type { FileNode } from "../../bindings/drivewise/backend/models";
import { formatBytes } from "../utils/format";
import { explainPath } from "../utils/pathExplain";

defineOptions({ name: "DirTreeRow" });

const props = defineProps<{
  node: FileNode;
  depth: number;
  onLoad: (node: FileNode) => Promise<void>;
}>();

const emit = defineEmits<{
  (e: "open", path: string): void;
  (e: "delete", path: string): void;
  (e: "refresh", path: string): void;
}>();

const expanded = ref(false);
const loaded = ref(false);
const loading = ref(false);

/* 右键菜单状态 */
const menuVisible = ref(false);
const menuX = ref(0);
const menuY = ref(0);

const isDir = computed(() => props.node.isDir);
const hasKids = computed(() => (props.node.children?.length ?? 0) > 0);
/** 小白解释：命中常见目录时显示 ⓘ */
const explainText = computed(() => explainPath(props.node.name));

function children(): FileNode[] {
  return (props.node.children ?? []).filter((c): c is FileNode => c !== null);
}

async function toggle() {
  if (!isDir.value) return;
  if (expanded.value) {
    expanded.value = false;
    return;
  }
  expanded.value = true;
  if (!loaded.value) {
    loading.value = true;
    try {
      await props.onLoad(props.node);
      loaded.value = true;
    } finally {
      loading.value = false;
    }
  }
}

/* 右键菜单 */
function showMenu(e: MouseEvent) {
  e.preventDefault();
  menuX.value = e.clientX;
  menuY.value = e.clientY;
  menuVisible.value = true;
}

function closeMenu() {
  menuVisible.value = false;
}

function onOpen() {
  closeMenu();
  emit("open", props.node.path);
}

function onDelete() {
  closeMenu();
  emit("delete", props.node.path);
}

onBeforeUnmount(() => closeMenu());
</script>

<template>
  <li>
    <div
      class="group flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 hover:bg-slate-100/70"
      :style="{ paddingLeft: `${12 + depth * 20}px` }"
      @click="toggle"
      @contextmenu="showMenu"
    >
      <!-- 展开箭头 -->
      <button
        v-if="isDir"
        class="flex h-4 w-4 shrink-0 items-center justify-center text-slate-400 transition-transform"
        :class="{ 'rotate-90': expanded }"
        :disabled="loading"
        @click.stop="toggle"
      >
        <svg v-if="!loading" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="h-3.5 w-3.5">
          <path d="m9 18 6-6-6-6" />
        </svg>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" class="h-3.5 w-3.5 animate-spin">
          <path d="M21 12a9 9 0 1 1-6.2-8.6" stroke-linecap="round" />
        </svg>
      </button>
      <span v-else class="w-4 shrink-0" />

      <!-- 图标 -->
      <span class="shrink-0 text-sm">{{ isDir ? "📁" : "📄" }}</span>

      <!-- 名称 -->
      <span
        class="min-w-0 flex-1 truncate text-sm"
        :class="isDir ? 'font-medium text-slate-700' : 'text-slate-600'"
        :title="node.path"
      >
        {{ node.name }}
      </span>

      <!-- 小白解释 ⓘ -->
      <span
        v-if="explainText"
        class="shrink-0 cursor-help rounded-full text-slate-300 transition-colors hover:text-blue-500"
        :title="explainText"
      >ⓘ</span>

      <!-- 文件数 -->
      <span v-if="isDir && node.children" class="shrink-0 text-xs text-slate-400">
        {{ node.children.filter((c) => c !== null).length }} 项
      </span>

      <!-- 大小 -->
      <span
        class="w-24 shrink-0 text-right text-sm font-medium tabular-nums"
        :class="
          node.size > 1024 * 1024 * 1024
            ? 'text-red-600'
            : node.size > 100 * 1024 * 1024
              ? 'text-amber-600'
              : 'text-slate-500'
        "
      >
        {{ formatBytes(node.size) }}
      </span>
    </div>

    <!-- 子节点 -->
    <ul v-if="expanded && isDir" class="border-l border-slate-100">
      <template v-if="hasKids">
        <DirTreeRow
          v-for="c in children()"
          :key="c.path"
          :node="c"
          :depth="depth + 1"
          :on-load="onLoad"
          @open="(p: string) => emit('open', p)"
          @delete="(p: string) => emit('delete', p)"
          @refresh="(p: string) => emit('refresh', p)"
        />
      </template>
      <li
        v-else-if="!loading"
        class="px-2 py-1 text-xs text-slate-400"
        :style="{ paddingLeft: `${34 + depth * 20}px` }"
      >
        （空目录或无可显示内容）
      </li>
    </ul>

    <!-- 右键菜单 -->
    <Teleport to="body">
      <div
        v-if="menuVisible"
        class="fixed z-50 w-48 overflow-hidden rounded-xl border border-slate-200 bg-white py-1 shadow-2xl shadow-slate-300/40"
        :style="{ left: menuX + 'px', top: menuY + 'px' }"
        @contextmenu.prevent
      >
        <div class="border-b border-slate-100 bg-slate-50/70 px-3 py-1.5">
          <div class="truncate text-[11px] font-semibold text-slate-700">{{ node.name }}</div>
          <div v-if="explainText" class="truncate text-[10px] text-slate-400">{{ explainText }}</div>
        </div>
        <button
          class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-slate-700 transition-colors hover:bg-blue-50 hover:text-blue-700"
          @click="onOpen"
        >
          <span class="text-base">📂</span> {{ isDir ? "打开目录" : "打开所在目录" }}
        </button>
        <div class="mx-3 my-1 border-t border-slate-100" />
        <button
          class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-red-600 transition-colors hover:bg-red-50"
          @click="onDelete"
        >
          <span class="text-base">🗑️</span> 移入回收站
        </button>
        <div class="border-t border-slate-100 bg-slate-50/50 px-3 py-1 text-[10px] text-slate-400">
          删除后可恢复 · 系统目录受保护
        </div>
      </div>
    </Teleport>

    <!-- 点击空白关闭菜单 -->
    <Teleport to="body">
      <div v-if="menuVisible" class="fixed inset-0 z-40" @click="closeMenu" @contextmenu.prevent="closeMenu" />
    </Teleport>
  </li>
</template>
