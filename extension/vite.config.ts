import { readFileSync } from "node:fs";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [
    {
      name: "extension-manifest",
      generateBundle() {
        this.emitFile({
          type: "asset",
          fileName: "manifest.json",
          source: readFileSync(
            new URL("./manifest.json", import.meta.url),
            "utf8",
          ),
        });
      },
    },
  ],
  build: {
    rollupOptions: {
      input: {
        background: "src/background/index.ts",
        content: "src/content/index.ts",
        popup: "src/popup/index.html",
      },
      output: {
        entryFileNames: "[name].js",
        assetFileNames: (asset) =>
          asset.names.includes("content.css")
            ? "content.css"
            : "assets/[name]-[hash][extname]",
      },
    },
  },
});
