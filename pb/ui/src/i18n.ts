// Two languages: Thai (the source text) and English. t() takes the Thai string itself as the key, so
// the code reads as before; {0}, {1}… are filled from the arguments in both languages. A string with
// no English entry stays Thai. The choice is stored per browser ("sq.lang"); changing it re-renders.
import { EN } from "./i18n-en";

export type Lang = "th" | "en";
export const getLang = (): Lang => (localStorage.getItem("sq.lang") as Lang) || "th";
const lang: Lang = getLang();
document.documentElement.lang = lang;

// Labels built when a module loads (tab names, event kinds) read the language then, so a switch
// reloads the page: one consistent language everywhere, no half-translated screen.
export function setLang(l: Lang) {
  localStorage.setItem("sq.lang", l);
  location.reload();
}

export function t(th: string, ...args: unknown[]): string {
  const s = lang === "en" ? EN[th] ?? th : th;
  return args.length ? s.replace(/\{(\d+)\}/g, (_: string, i: string) => String(args[+i] ?? "")) : s;
}

// Dates and times follow the language too.
export const locale = () => (lang === "en" ? "en-GB" : "th-TH");
