<script setup lang="ts">
defineProps<{
  active: string;
}>();

const emit = defineEmits<{
  select: [tab: string];
}>();

interface NavItem {
  key: string;
  label: string;
  desc: string;
  icon: string;
  badge?: string;
}

const navItems: NavItem[] = [
  {
    key: "analyzer",
    label: "磁盘分析",
    desc: "极速空间占用",
    icon: "M3 3v18h18M7 15l4-6 3 4 5-8",
  },
  {
    key: "cache",
    label: "缓存清理",
    desc: "常规垃圾清理",
    icon: "M3 6h18M5 6v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V6M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M9 11v6M15 11v6",
  },
  {
    key: "rogue",
    label: "流氓软件",
    desc: "识别与清理",
    icon: "M12 22s8-3 8-10V5l-8-3-8 3v7c0 7 8 10 8 10zM9 12l2 2 4-4",
  },
  {
    key: "migrator",
    label: "软件迁移",
    desc: "应用搬离 C 盘",
    icon: "M4 7h16M4 12h16M4 17h10M14 21l5-5 5 5",
  },
];
</script>

<template>
  <aside
    class="flex w-60 shrink-0 flex-col border-r border-slate-200 bg-white"
  >
    <!-- Logo -->
    <div class="flex items-center gap-3 px-5 py-5">
      <div
        class="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-sky-500 to-blue-600 shadow-md shadow-blue-200"
      >
        <!-- DriveWise logo: disk platter + checkmark -->
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="white"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          class="h-5 w-5"
        >
          <!-- Disk platter ring -->
          <circle cx="12" cy="12" r="9" stroke-opacity="0.7" />
          <!-- Platter hub -->
          <circle cx="12" cy="12" r="2.5" fill="white" fill-opacity="0.3" stroke="none" />
          <!-- Checkmark (wise = clean) -->
          <path d="M 8.5 12.5 L 11 15 L 16 9.5" stroke-width="2.4" />
        </svg>
      </div>
      <div>
        <div class="text-base font-bold text-slate-800">DriveWise</div>
        <div class="text-xs text-slate-400">C 盘智能清理</div>
      </div>
    </div>

    <!-- 导航 -->
    <nav class="mt-2 flex-1 space-y-1 px-3">
      <button
        v-for="item in navItems"
        :key="item.key"
        class="group flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left transition-colors"
        :class="
          active === item.key
            ? 'bg-gradient-to-r from-blue-600 to-indigo-600 text-white shadow-md shadow-blue-200'
            : 'text-slate-600 hover:bg-slate-100'
        "
        @click="emit('select', item.key)"
      >
        <svg
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          class="h-5 w-5 shrink-0"
        >
          <path :d="item.icon" />
        </svg>
        <span class="flex-1">
          <span class="block text-sm font-medium">{{ item.label }}</span>
          <span
            class="block text-xs"
            :class="active === item.key ? 'text-blue-200' : 'text-slate-400'"
          >
            {{ item.desc }}
          </span>
        </span>
        <span
          v-if="item.badge"
          class="rounded-full bg-amber-100 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600"
        >
          {{ item.badge }}
        </span>
      </button>
    </nav>

    <!-- 底部 -->
    <div class="border-t border-slate-100 px-5 py-3">
      <div class="flex items-center gap-2 text-xs text-slate-400">
        <span class="inline-block h-2 w-2 rounded-full bg-emerald-400"></span>
        服务运行中 · v0.1.0
      </div>
    </div>
  </aside>
</template>
