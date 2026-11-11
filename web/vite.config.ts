import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build is embedded into the Go binary (internal/core/webui/dist).
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "../internal/core/webui/dist",
    emptyOutDir: false,
    target: "es2022",
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: { "/api": { target: "http://127.0.0.1:7780", changeOrigin: false } },
  },
});
