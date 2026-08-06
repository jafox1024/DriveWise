import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  build: {
    // 手动清理 dist（WorkBuddy 安全删除钩子会拦截 rmSync 导致 emptyOutDir 失败）
    emptyOutDir: false,
  },
  plugins: [vue(), tailwindcss(), wails("./bindings")],
});
