// Builds the plugin main thread (src/main.ts) into dist/code.js.
import { defineConfig } from "vite";

export default defineConfig({
  build: {
    target: "es2017",
    lib: {
      entry: "src/main.ts",
      formats: ["iife"],
      name: "code",
      fileName: () => "code.js",
    },
    outDir: "dist",
    emptyOutDir: false,
    minify: false,
  },
});
