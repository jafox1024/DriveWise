<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { MigratorService } from "../../bindings/drivewise/backend/migrator";
import { AnalyzerService } from "../../bindings/drivewise/backend/analyzer";
import type { MigratableApp, DriveInfo } from "../../bindings/drivewise/backend/models";
import type { MigrateRecord } from "../../bindings/drivewise/backend/migrator/models";
import { formatBytes, formatCount } from "../utils/format";

const apps = ref<MigratableApp[]>([]);
const migrations = ref<MigrateRecord[]>([]);
const drives = ref<DriveInfo[]>([]);
const targetDrive = ref<string>("");
const scanning = ref(false);
const movingId = ref<string>("");
const restoringId = ref<string>("");
const message = ref<{ type: "ok" | "err"; text: string } | null>(null);

const otherDrives = computed(() =>
  drives.value.filter((d) => !d.name.startsWith("C")),
);

onMounted(async () => {
  drives.value = (await AnalyzerService.GetDrives()) ?? [];
  if (otherDrives.value.length) {
    targetDrive.value = otherDrives.value[0].name + "\\";
  }
  await Promise.all([scanApps(), loadMigrations()]);
});

async function scanApps() {
  scanning.value = true;
  message.value = null;
  try {
    apps.value = (await MigratorService.ScanApps()) ?? [];
  } catch (e) {
    message.value = { type: "err", text: `扫描失败：${String(e)}` };
  } finally {
    scanning.value = false;
  }
}

async function loadMigrations() {
  migrations.value = (await MigratorService.GetMigrations()) ?? [];
}

async function moveApp(app: MigratableApp) {
  if (!targetDrive.value) {
    message.value = { type: "err", text: "请先选择目标磁盘" };
    return;
  }
  movingId.value = app.path;
  message.value = null;
  const dst = `${targetDrive.value}DriveWise_Moved\\${app.name}`;
  try {
    await MigratorService.MoveApp(app.path, dst);
    message.value = {
      type: "ok",
      text: `「${app.name}」已迁移至 ${dst}`,
    };
    await Promise.all([scanApps(), loadMigrations()]);
  } catch (e) {
    message.value = { type: "err", text: `迁移失败：${String(e)}` };
  } finally {
    movingId.value = "";
  }
}

async function restoreApp(rec: MigrateRecord) {
  restoringId.value = rec.srcPath;
  message.value = null;
  try {
    await MigratorService.RestoreApp(rec.srcPath);
    message.value = { type: "ok", text: `「${rec.name}」已恢复至 C 盘` };
    await Promise.all([scanApps(), loadMigrations()]);
  } catch (e) {
    message.value = { type: "err", text: `恢复失败：${String(e)}` };
  } finally {
    restoringId.value = "";
  }
}
</script>

<template>
  <div class="mx-auto max-w-5xl p-8">
    <header class="mb-6">
      <h1 class="text-2xl font-bold text-slate-800">软件迁移</h1>
      <p class="mt-1 text-sm text-slate-500">
        将大型应用目录迁移到其他磁盘，原位置保留目录联接，应用无需重装、数据无缝读写。
      </p>
    </header>

    <!-- 提示 -->
    <div
      v-if="message"
      class="mb-5 rounded-xl border p-4 text-sm shadow-sm"
      :class="message.type === 'ok' ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : 'border-red-200 bg-red-50 text-red-700'"
    >
      {{ message.text }}
    </div>

    <!-- 操作栏 -->
    <div class="mb-5 flex items-center justify-between gap-4 rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
      <button
        class="rounded-lg bg-blue-600 px-5 py-2 text-sm font-medium text-white shadow-sm shadow-blue-200 transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
        :disabled="scanning"
        @click="scanApps"
      >
        {{ scanning ? "扫描中..." : "重新扫描" }}
      </button>
      <div class="flex items-center gap-2 text-sm text-slate-600">
        <span>迁移到：</span>
        <select
          v-model="targetDrive"
          class="rounded-lg border border-slate-300 px-3 py-1.5 text-sm focus:border-blue-500 focus:outline-none"
        >
          <option v-for="d in otherDrives" :key="d.name" :value="d.name + '\\'">
            {{ d.name }}（可用 {{ formatBytes(d.free) }}）
          </option>
          <option v-if="otherDrives.length === 0" disabled>未检测到其他磁盘</option>
        </select>
      </div>
    </div>

    <!-- 已迁移列表 -->
    <div v-if="migrations.length" class="mb-6 rounded-xl border border-sky-200 bg-sky-50 p-4">
      <div class="mb-3 text-sm font-semibold text-sky-700">
        已迁移（{{ migrations.length }}）— 点击恢复可将数据移回 C 盘
      </div>
      <ul class="space-y-2">
        <li
          v-for="rec in migrations"
          :key="rec.id"
          class="flex items-center justify-between gap-3 rounded-lg bg-white px-3 py-2 text-sm shadow-sm"
        >
          <div class="min-w-0 flex-1">
            <span class="font-medium text-slate-700">{{ rec.name }}</span>
            <span class="ml-2 text-xs text-slate-400">{{ rec.time }}</span>
            <div class="truncate text-xs text-slate-400">
              {{ rec.srcPath }} → {{ rec.targetPath }}
            </div>
          </div>
          <button
            class="shrink-0 rounded-md border border-sky-300 px-3 py-1 text-xs text-sky-600 hover:bg-sky-100 disabled:opacity-50"
            :disabled="restoringId === rec.srcPath"
            @click="restoreApp(rec)"
          >
            {{ restoringId === rec.srcPath ? "恢复中..." : "恢复" }}
          </button>
        </li>
      </ul>
    </div>

    <!-- 应用列表 -->
    <div class="space-y-3">
      <div
        v-if="apps.length === 0 && !scanning"
        class="flex flex-col items-center justify-center rounded-xl border-2 border-dashed border-slate-200 bg-white py-20 text-slate-400"
      >
        <span class="mb-3 text-4xl">📦</span>
        <p class="text-sm">未检测到可迁移的应用目录（默认只列出大于 100MB 的 AppData 目录）</p>
      </div>

      <div
        v-for="app in apps"
        :key="app.path"
        class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm"
      >
        <div class="flex items-center gap-3">
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <span class="text-sm font-semibold text-slate-700">{{ app.name }}</span>
              <span
                class="rounded-lg px-2 py-0.5 text-xs font-semibold"
                :class="app.size > 1024 * 1024 * 1024 ? 'bg-red-50 text-red-600' : 'bg-amber-50 text-amber-600'"
              >
                {{ formatBytes(app.size) }}
              </span>
            </div>
            <div class="mt-0.5 truncate font-mono text-xs text-slate-400">{{ app.path }}</div>
            <div class="mt-0.5 text-xs text-slate-400">{{ formatCount(app.fileCount) }} 个文件</div>
          </div>
          <button
            class="shrink-0 rounded-lg bg-blue-600 px-4 py-1.5 text-sm font-medium text-white shadow-sm shadow-blue-200 transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-50"
            :disabled="movingId === app.path || otherDrives.length === 0"
            @click="moveApp(app)"
          >
            {{ movingId === app.path ? "迁移中..." : "迁移" }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
