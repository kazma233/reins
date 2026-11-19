import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import wails from "@wailsio/runtime/plugins/vite";

export default defineConfig({
  plugins: [vue(), wails("./bindings")],
  resolve: {
    alias: {
      "@shared": fileURLToPath(new URL("./src/shared", import.meta.url)),
      // bindings 目录别名：src 深处的 api.ts 引用生成物不用写多级相对路径。
      "@bindings": fileURLToPath(new URL("./bindings", import.meta.url)),
    },
  },
  clearScreen: false,
  server: {
    host: "127.0.0.1",
    // 端口由 wails3 dev 注入的 WAILS_VITE_PORT 决定，直跑时用同一默认值。
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
});
