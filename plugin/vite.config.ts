// Builds the plugin UI (src/ui/index.html) into a single inlined dist/index.html.
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export default defineConfig({
  plugins: [viteSingleFile()],
  root: "./src/ui",
  build: {
    target: "es2017",
    cssCodeSplit: false,
    outDir: "../../dist",
    emptyOutDir: true,
    rollupOptions: {
      output: { inlineDynamicImports: true },
    },
  },
});
