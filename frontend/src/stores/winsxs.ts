import { defineStore } from "pinia";
import { ref, computed } from "vue";
import { CacheService } from "../../bindings/drivewise/backend/cleaner";
import type { WinSxSCleanStatus } from "../../bindings/drivewise/backend/models";

/**
 * WinSxS 后台清理状态（全局单例）
 *
 * 状态与轮询放在 Pinia store 中，与页面组件生命周期解耦：
 * 切换页面时组件销毁不会中断轮询；切回时通过 ensurePolling()
 * 拉取一次后端状态并续上轮询，进度不会丢失。
 */
export const useWinSxSStore = defineStore("winsxs", () => {
  const status = ref<WinSxSCleanStatus | null>(null);
  const error = ref("");
  const apiBusy = ref(false);
  let pollTimer: ReturnType<typeof setInterval> | null = null;

  const running = computed(() => !!status.value?.running);
  const done = computed(() => !!status.value?.done);

  /** 轮询后台状态（每 2.5 秒），完成后自动停止 */
  function startPolling() {
    stopPolling();
    pollTimer = setInterval(async () => {
      try {
        status.value = await CacheService.GetWinSxSStatus();
        if (status.value?.done) stopPolling();
      } catch {
        /* 网络层错误忽略，继续轮询 */
      }
    }, 2500);
  }

  function stopPolling() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  /**
   * 页面挂载时调用：拉取一次后端状态，若清理仍在后台运行则续上轮询，
   * 若已结束则展示结果。
   */
  async function ensurePolling() {
    try {
      status.value = await CacheService.GetWinSxSStatus();
      if (status.value?.running) startPolling();
      else if (status.value?.done) stopPolling();
    } catch {
      /* 网络层错误忽略 */
    }
  }

  /** 启动后台清理并开始轮询 */
  async function startClean() {
    apiBusy.value = true;
    error.value = "";
    try {
      status.value = await CacheService.StartWinSxSClean();
      startPolling();
    } catch (e) {
      error.value = `启动失败：${String(e)}`;
    } finally {
      apiBusy.value = false;
    }
  }

  return {
    status,
    error,
    apiBusy,
    running,
    done,
    ensurePolling,
    startClean,
    stopPolling,
  };
});
