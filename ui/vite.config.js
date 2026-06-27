import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

// Two build modes:
//   - default  →  the demo app (dist/), a runnable showcase of <AppsAndBots/>
//   - LIB=1    →  the distributable library (dist-lib/), React externalized
const isLib = !!process.env.LIB;

export default defineConfig({
  plugins: [react()],
  ...(isLib
    ? {
        build: {
          outDir: "dist-lib",
          emptyOutDir: true,
          lib: {
            entry: resolve(__dirname, "src/index.js"),
            name: "VulosAppsUI",
            fileName: "vulos-apps-ui",
            formats: ["es", "umd"],
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
});
