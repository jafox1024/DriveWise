<script setup lang="ts">
import { onMounted, ref } from "vue";
import Sidebar from "./components/Sidebar.vue";
import AnalyzerPage from "./pages/AnalyzerPage.vue";
import CachePage from "./pages/CachePage.vue";
import RoguePage from "./pages/RoguePage.vue";
import MigratorPage from "./pages/MigratorPage.vue";
import { SysService } from "../bindings/drivewise/backend/sysinfo";

type Tab = "analyzer" | "cache" | "rogue" | "migrator";

const activeTab = ref<Tab>("cache");
const isAdmin = ref(true);
const adminChecked = ref(false);
const restarting = ref(false);

onMounted(async () => {
  try {
    isAdmin.value = await SysService.IsAdmin();
  } catch {
    isAdmin.value = false;
  }
  adminChecked.value = true;
});

async function restartAsAdmin() {
  restarting.value = true;
  try {
    await SysService.RestartAsAdmin();
  } catch (e) {
    restarting.value = false;
    alert(`提权重启失败：${String(e)}`);
  }
}
</script>

<template>
  <div class="flex h-screen flex-col bg-gradient-to-br from-slate-100 via-slate-50 to-blue-50/60 text-slate-800">
    <!-- 管理员权限横幅 -->
    <div
      v-if="adminChecked && !isAdmin"
      class="flex items-center justify-center gap-3 border-b border-amber-200 bg-amber-50 px-4 py-2 text-sm text-amber-700"
    >
      <span>⚠️ 当前以普通权限运行，清理系统临时目录、WinSxS、注册表（HKLM）等操作可能受限</span>
      <button
        class="rounded-lg bg-amber-500 px-4 py-1 text-xs font-medium text-white transition-colors hover:bg-amber-600 disabled:cursor-not-allowed disabled:opacity-60"
        :disabled="restarting"
        @click="restartAsAdmin"
      >
        {{ restarting ? "正在请求提权..." : "以管理员身份重启" }}
      </button>
    </div>

    <div class="flex min-h-0 flex-1">
      <Sidebar :active="activeTab" @select="(t: string) => (activeTab = t as Tab)" />
      <main class="flex-1 overflow-y-auto">
        <Transition name="fade" mode="out-in">
          <CachePage v-if="activeTab === 'cache'" key="cache" />
          <AnalyzerPage v-else-if="activeTab === 'analyzer'" key="analyzer" />
          <RoguePage v-else-if="activeTab === 'rogue'" key="rogue" />
          <MigratorPage v-else-if="activeTab === 'migrator'" key="migrator" />
        </Transition>
      </main>
    </div>
  </div>
</template>
