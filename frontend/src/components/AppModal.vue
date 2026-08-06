<script setup lang="ts">
defineProps<{
  open: boolean;
  title: string;
  subtitle?: string;
  confirmText?: string;
  cancelText?: string;
  tone?: "primary" | "danger" | "violet";
  loading?: boolean;
}>();

const emit = defineEmits<{
  (e: "confirm"): void;
  (e: "cancel"): void;
}>();

const toneClass: Record<string, string> = {
  primary:
    "bg-blue-600 text-white shadow-md shadow-blue-200 hover:bg-blue-700",
  danger:
    "bg-red-600 text-white shadow-md shadow-red-200 hover:bg-red-700",
  violet:
    "bg-violet-600 text-white shadow-md shadow-violet-200 hover:bg-violet-700",
};
</script>

<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="open" class="fixed inset-0 z-[100] flex items-center justify-center p-4">
        <!-- 遮罩 -->
        <div class="absolute inset-0 bg-slate-900/40 backdrop-blur-[2px]" @click="emit('cancel')" />

        <!-- 卡片 -->
        <div class="relative w-full max-w-md overflow-hidden rounded-2xl bg-white shadow-2xl ring-1 ring-slate-900/5">
          <!-- 标题区 -->
          <div class="border-b border-slate-100 bg-gradient-to-r from-slate-50 via-white to-white px-6 py-4">
            <h3 class="flex items-center gap-2 text-base font-semibold text-slate-800">
              <slot name="title-icon" />
              {{ title }}
            </h3>
            <p v-if="subtitle" class="mt-1 text-xs leading-relaxed text-slate-500">{{ subtitle }}</p>
          </div>

          <!-- 内容区 -->
          <div class="max-h-[55vh] overflow-y-auto px-6 py-4 text-sm leading-relaxed text-slate-600">
            <slot />
          </div>

          <!-- 操作区 -->
          <div class="flex items-center justify-end gap-3 border-t border-slate-100 bg-slate-50/70 px-6 py-4">
            <button
              class="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-slate-100"
              @click="emit('cancel')"
            >
              {{ cancelText ?? "取消" }}
            </button>
            <button
              class="rounded-lg px-5 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50"
              :class="toneClass[tone ?? 'primary']"
              :disabled="loading"
              @click="emit('confirm')"
            >
              {{ loading ? "处理中..." : (confirmText ?? "确定") }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.18s ease;
}
.modal-enter-active .relative,
.modal-leave-active .relative {
  transition:
    transform 0.18s ease,
    opacity 0.18s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from .relative,
.modal-leave-to .relative {
  transform: translateY(14px) scale(0.97);
  opacity: 0;
}
</style>
