import { fileURLToPath, URL } from "node:url"

import { defineConfig } from "vite"

export default defineConfig({
  root: fileURLToPath(new URL("./emergency", import.meta.url)),
  build: {
    outDir: fileURLToPath(new URL("../internal/webui/dist/emergency", import.meta.url)),
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        entryFileNames: "assets/emergency.js",
        chunkFileNames: "assets/emergency-chunk-[name].js",
        assetFileNames: (assetInfo) =>
          assetInfo.names.some((name) => name.endsWith(".css"))
            ? "assets/emergency.css"
            : "assets/[name][extname]",
      },
    },
  },
})
