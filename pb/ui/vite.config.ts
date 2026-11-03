import { defineConfig } from "vite";
import preact from "@preact/preset-vite";
import tailwindcss from "@tailwindcss/vite";

// The review page + chat tabs. Built ahead of time and embedded in the binary (sqweb.go):
// no script is fetched from a CDN at run time, because this page holds the approve button.
// dev: vite on :5173 proxies the PocketBase API to a running `messenger-pb serve`.
const API = process.env.PB_URL ?? "http://phone.example.ts.net:8097";

export default defineConfig({
  plugins: [preact(), tailwindcss()],
  build: { outDir: "../sqweb/dist", emptyOutDir: true, assetsDir: "assets" },
  server: { proxy: { "/api": API, "/chat": API } },
});
