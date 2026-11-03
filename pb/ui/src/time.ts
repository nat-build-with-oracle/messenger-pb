import { locale, t } from "./i18n";
const clock = (ms: number) => new Date(ms).toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" });

// "14:02", "ศ. 14:02", "9 ต.ค. 14:02", "9 ต.ค. 2568 14:02"
export function stamp(ms: number) {
  const d = new Date(ms), now = new Date();
  if (d.toDateString() === now.toDateString()) return clock(ms);
  if ((+now - +d) / 864e5 < 6) return d.toLocaleDateString(locale(), { weekday: "short" }) + " " + clock(ms);
  const sameYear = d.getFullYear() === now.getFullYear();
  return d.toLocaleDateString(locale(), { day: "numeric", month: "short", year: sameYear ? undefined : "numeric" }) + " " + clock(ms);
}

export function ago(ms: number) {
  const m = Math.max(0, Math.round((Date.now() - ms) / 6e4));
  if (m < 1) return t("เมื่อกี้");
  if (m < 60) return m + t(" นาที");
  if (m < 1440) return Math.floor(m / 60) + t(" ชม.");
  if (m < 10080) return Math.floor(m / 1440) + t(" วัน");
  return Math.floor(m / 10080) + t(" สัปดาห์");
}

// PocketBase dates: "2026-10-24 07:03:55.123Z"
export const pbDate = (d: string) =>
  d ? new Date(d.replace(" ", "T")).toLocaleString(locale(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" }) : "";
