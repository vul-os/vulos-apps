/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import dts from "vite-plugin-dts";
import { resolve } from "path";

// Two build modes:
//   - default  →  the demo app (dist/), a runnable showcase of <AppsAndBots/>
//   - LIB=1    →  the distributable library (dist-lib/): JS (ESM + UMD) + .d.ts,
//                 React externalized so consumers dedupe a single instance.
const isLib = !!process.env.LIB;

export default defineConfig({
  plugins: [
    react(),
    ...(isLib
      ? [
          dts({
            include: ["src"],
            exclude: ["src/**/*.test.ts", "src/demo/**"],
            outDir: "dist-lib",
          }),
        ]
      : []),
  ],
  ...(isLib
    ? {
        build: {
          outDir: "dist-lib",
          emptyOutDir: true,
          lib: {
            entry: resolve(__dirname, "src/index.ts"),
            name: "VulosAppsUI",
            fileName: "vulos-apps-ui",
            formats: ["es", "umd"] as const,
          },
          rollupOptions: {
            external: ["react", "react-dom", "react/jsx-runtime"],
            output: {
              exports: "named",
              assetFileNames: "vulos-apps-ui[extname]",
              globals: {
                react: "React",
                "react-dom": "ReactDOM",
                "react/jsx-runtime": "jsxRuntime",
              },
            },
          },
        },
      }
    : {
        build: { outDir: "dist", emptyOutDir: true },
      }),
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
