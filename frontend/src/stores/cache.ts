import { defineStore } from "pinia";
import { ref, computed } from "vue";
import { CacheService } from "../../bindings/drivewise/backend/cleaner";
import type {
  CacheCategory,
  CleanResult,
} from "../../bindings/drivewise/backend/models";

export const useCacheStore = defineStore("cache", () => {
  const categories = ref<CacheCategory[]>([]);
  const scanning = ref(false);
  const cleaning = ref(false);
  const lastResults = ref<CleanResult[]>([]);
  const lastScanTime = ref<string>("");

  /** 全部可清理空间（数据保护分类不计入） */
  const totalSize = computed(() =>
    categories.value.reduce((s, c) => s + (c.size || 0), 0),
  );
  /** 可安全清理的空间（排除数据保护分类） */
  const cleanableSize = computed(() =>
    categories.value
      .filter((c) => !c.dataOnly)
      .reduce((s, c) => s + (c.size || 0), 0),
  );
  /** 选中项空间（数据保护分类不可选，不计入） */
  const selectedSize = computed(() =>
    categories.value
      .filter((c) => c.selected && !c.dataOnly)
      .reduce((s, c) => s + (c.size || 0), 0),
  );
  /** 数据保护分类总占用（聊天/办公数据，仅展示提示迁移） */
  const dataSize = computed(() =>
    categories.value
      .filter((c) => c.dataOnly)
      .reduce((s, c) => s + (c.size || 0), 0),
  );

  async function scan() {
    scanning.value = true;
    try {
      const result = await CacheService.ScanCache();
      categories.value = (result ?? []).map((c) => ({
        ...c,
        // 数据保护分类强制不可勾选，防止误清理
        selected: c.dataOnly ? false : (c.selected ?? false),
      }));
      lastScanTime.value = new Date().toLocaleTimeString();
    } finally {
      scanning.value = false;
    }
  }

  async function clean() {
    const names = categories.value
      .filter((c) => c.selected && !c.dataOnly)
      .map((c) => c.name);
    if (!names.length) return;
    cleaning.value = true;
    try {
      const results = await CacheService.CleanCache(names);
      lastResults.value = results ?? [];
      // 清理后重新扫描，刷新大小
      await scan();
    } finally {
      cleaning.value = false;
    }
  }

  function toggleAll(checked: boolean) {
    categories.value.forEach((c) => {
      // 数据保护分类不参与全选
      if (!c.dataOnly) c.selected = checked;
    });
  }

  return {
    categories,
    scanning,
    cleaning,
    lastResults,
    lastScanTime,
    totalSize,
    cleanableSize,
    selectedSize,
    dataSize,
    scan,
    clean,
    toggleAll,
  };
});
