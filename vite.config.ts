import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig(({ command }) => ({
  base: process.env.VITE_BASE_PATH || (command === "build" ? "/mail/" : "/"),
  plugins: [react()],
  resolve: {
    alias: [
      { find: "@aillive/mail-ui/routes", replacement: path.resolve(__dirname, "packages/mail-ui/src/routes.ts") },
      { find: "@aillive/mail-ui", replacement: path.resolve(__dirname, "packages/mail-ui/src/index.ts") },
      { find: "@aillive/api-types", replacement: path.resolve(__dirname, "packages/api-types/src/index.ts") },
    ],
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:3001",
    },
  },
  build: {
    outDir: "dist",
  },
}));
