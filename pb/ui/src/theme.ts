import { t } from "./i18n";
// Theme: auto (follow the system), dark, light or paper. Stored per browser in localStorage
// ("sq.theme") and applied as html[data-theme] before the first render, so nothing flashes.
export type Theme = "auto" | "dark" | "light" | "paper";
export const THEMES: [Theme, string][] = [["auto", t("ตามระบบ")], ["dark", t("มืด")], ["light", t("สว่าง")], ["paper", t("กระดาษ")]];

const media = matchMedia("(prefers-color-scheme: light)");
export const getTheme = (): Theme => (localStorage.getItem("sq.theme") as Theme) || "auto";
function apply() {
  const t = getTheme();
  document.documentElement.dataset.theme = t === "auto" ? (media.matches ? "light" : "dark") : t;
}
export function setTheme(t: Theme) {
  localStorage.setItem("sq.theme", t);
  apply();
  dispatchEvent(new Event("sq-theme")); // the top-bar button and the account panel stay in step
}
export function initTheme() {
  apply();
  media.addEventListener("change", () => getTheme() === "auto" && apply());
}
