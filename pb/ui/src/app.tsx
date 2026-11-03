import { Fragment } from "preact";
import { useEffect, useState } from "preact/hooks";
import { getAuth, login, loginFromLink, loginWithPasskey, logout, onAuth, passkeySupported, refreshAuth, type Auth, openAdmin } from "./api";
import { Account } from "./passkey";
import { getTheme, setTheme, THEMES, type Theme } from "./theme";
import { useHash } from "./hooks";
import { Queue, usePendingCount } from "./queue";
import { ChatView, CHAT_TABS, routeOf } from "./chat";
import { Logs } from "./logs";
import { getLang, setLang, t } from "./i18n";
import { privacy, pvEmail, setPrivacy } from "./privacy";

type Tab = string;

export function App() {
  const [auth, setAuth] = useState(getAuth());
  useEffect(() => onAuth(() => setAuth(getAuth())), []);
  const [linkErr, setLinkErr] = useState("");
  useEffect(() => void loginFromLink().then(refreshAuth, (x) => setLinkErr(x.message)), []);
  const hash = useHash();
  const route = routeOf(hash);
  // #<send id> opens that draft under ทั้งหมด; #a= / #c= / #l= open a chat in its tab.
  let [tab, setTab] = useState<Tab>(route?.tab || (hash.length > 1 && !hash.startsWith("#login=") ? "all" : localStorage.getItem("sq.tab") || "pending"));
  useEffect(() => void (route && setTab(route.tab)), [route?.tab]);
  const pick = (t: Tab) => {
    setTab(t);
    localStorage.setItem("sq.tab", t);
    history.replaceState(null, "", "/");
    dispatchEvent(new HashChangeEvent("hashchange"));
  };

  if (!auth) return <Login linkErr={linkErr} />;
  // a page this role does not have (a viewer's saved tab, a draft link) opens the inbox
  if (!pagesFor(auth).some((p) => p.tab === tab)) tab = "inbox";
  const chat = CHAT_TABS.some(([t]) => t === tab);
  return (
    <div class="flex h-dvh flex-col">
      <TopBar tab={tab} pick={pick} />
      <div class="flex min-h-0 flex-1 max-md:flex-col-reverse">
        <Rail tab={tab} pick={pick} />
        <div class="flex min-h-0 min-w-0 flex-1 flex-col">
          {chat ? <ChatView tab={tab} /> : tab === "logs" ? <Logs /> : <Queue tab={tab} hash={hash} pick={pick} />}
        </div>
      </div>
    </div>
  );
}

// Left icon rail like messenger.com (a bottom bar on a phone): every page, chats first, then
// the queue's pages with the pending count, then the account.
const I = (d: string, fill = false) => (
  <path d={d} fill={fill ? "currentColor" : "none"} stroke={fill ? "none" : "currentColor"} />
);
const PAGES: { tab: Tab; label: string; icon: any; color?: string }[] = [
  { tab: "inbox", label: t("รวมทุกแชท"), icon: I("M4 5h16v10h-4l-2 3h-4l-2-3H4z M4 15v4h16v-4") },
  { tab: "chats", label: "Messenger", color: "text-mine",
    icon: I("M12 3C7 3 3 6.6 3 11c0 2.4 1.2 4.6 3.2 6.1V21l3-1.7c.9.2 1.8.3 2.8.3 5 0 9-3.6 9-8.1S17 3 12 3zm1 10.5-2.3-2.4L6.5 13.5l4.6-4.9 2.3 2.4 4.1-2.4z", true) },
  { tab: "line", label: "LINE", color: "text-[#06c755]",
    icon: I("M12 3.5c-5 0-9 3.2-9 7.2 0 3.6 3.2 6.6 7.5 7.1.3.1.7.2.8.5.1.2 0 .6 0 .9l-.1.8c0 .2-.2.9.8.5s5.4-3.2 7.4-5.4C20.8 13.6 21 12.2 21 10.7c0-4-4-7.2-9-7.2z", true) },
  { tab: "pending", label: t("รอตรวจ"), color: "text-amber", icon: I("M12 7v5l3 2 M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z") },
  { tab: "approved", label: t("อนุมัติแล้ว"), color: "text-green", icon: I("M8 12.5l2.7 2.7L16 9.8 M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z") },
  { tab: "done", label: t("ส่งแล้ว"), color: "text-blue", icon: I("M21 3 10 14 M21 3l-7 18-4-7-7-4z") },
  { tab: "rejected", label: t("ไม่อนุมัติ"), color: "text-red", icon: I("M9 9l6 6M15 9l-6 6 M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z") },
  { tab: "all", label: t("ทั้งหมดในคิว"), icon: I("M8 6h12M8 12h12M8 18h12M4 6h.01M4 12h.01M4 18h.01") },
  { tab: "archived", label: t("เก็บแล้ว"), icon: I("M3 4h18v4H3z M5 8v12h14V8 M10 12h4") },
  { tab: "logs", label: t("บันทึก"), icon: I("M4 4h16v16H4z M8 9h8M8 13h8M8 17h5") },
];

// A viewer (login link) reads chats only: no queue pages, no logs.
const pagesFor = (auth: Auth) => (auth.role === "viewer" ? PAGES.slice(0, 3) : PAGES);
const roleName = (r: string) => ({ reviewer: t("ผู้ตรวจ"), viewer: t("ผู้อ่าน") })[r] || r || t("ไม่มีสิทธิ์");

function Rail({ tab, pick }: { tab: Tab; pick: (t: Tab) => void }) {
  const auth = getAuth()!;
  const pending = usePendingCount();
  const [account, setAccount] = useState(false);
  return (
    <nav class="flex flex-none items-center gap-1 bg-bg px-2 py-3 md:w-[68px] md:flex-col md:overflow-y-auto max-md:overflow-x-auto max-md:border-t max-md:border-rule max-md:py-1.5">
      {pagesFor(auth).map((p, i) => {
        const on = p.tab === tab;
        return (
          <Fragment key={p.tab}>
            {(i === 3 || p.tab === "logs") && <span class="flex-none bg-rule md:my-1.5 md:h-px md:w-8 max-md:mx-1 max-md:h-8 max-md:w-px" />}
            <button onClick={() => pick(p.tab)} title={p.label} aria-label={p.label} aria-current={on ? "page" : undefined}
              class={`group relative grid size-12 flex-none place-items-center rounded-xl ${on ? "bg-active text-ink" : "text-muted hover:bg-hover hover:text-ink"}`}>
              <svg viewBox="0 0 24 24" class={`size-6 ${on && p.color ? p.color : ""}`} stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">{p.icon}</svg>
              {p.tab === "pending" && pending > 0 && (
                <span class="absolute right-0.5 top-0.5 grid min-w-[18px] place-items-center rounded-full bg-amber px-1 text-[11px] font-bold text-black">{pending}</span>
              )}
              <span class="pointer-events-none absolute left-[56px] z-20 hidden whitespace-nowrap rounded-md bg-raised px-2 py-1 text-xs text-ink md:group-hover:block">{p.label}</span>
            </button>
          </Fragment>
        );
      })}
      <span class="md:mt-auto max-md:ml-auto" />
      {auth.col === "_superusers" && (
        <a href="/_/" target="_blank" rel="noopener" onClick={(e) => (e.preventDefault(), openAdmin())} title="PocketBase admin" aria-label="PocketBase admin"
          class="group relative grid size-12 flex-none place-items-center rounded-xl text-muted hover:bg-hover hover:text-ink">
          <svg viewBox="0 0 24 24" class="size-6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
            <path d="M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3z M4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6 M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6" />
          </svg>
          <span class="pointer-events-none absolute left-[56px] z-20 hidden whitespace-nowrap rounded-md bg-raised px-2 py-1 text-xs text-ink md:group-hover:block">PocketBase admin</span>
        </a>
      )}
      <button onClick={() => setAccount(true)} title={t("{0} · {1} — บัญชี, passkey, ออก", auth.email, roleName(auth.role))} aria-label={t("บัญชี")}
        class="grid size-10 flex-none place-items-center rounded-full bg-raised text-sm font-semibold uppercase text-ink hover:bg-raised-hi">
        {auth.email[0]}
      </button>
      {account && <Account close={() => setAccount(false)} />}
    </nav>
  );
}

// The same pages as words across the top, each with its rail icon, and the account on the right
// (one button: the panel holds passkeys and sign-out).
function TopBar({ tab, pick }: { tab: Tab; pick: (t: Tab) => void }) {
  const auth = getAuth()!;
  const pending = usePendingCount();
  const [account, setAccount] = useState(false);
  return (
    <header class="flex flex-none items-center gap-3 border-b border-rule bg-bg px-5 py-2.5">
      <button onClick={() => pick("inbox")} class="flex flex-none items-center gap-2 text-[15px] font-semibold tracking-tight hover:text-amber" title={t("หน้าแรก")}>
        <span class="grid size-7 place-items-center rounded-lg bg-amber/15 text-amber">
          <svg viewBox="0 0 24 24" class="size-4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 3 10 14 M21 3l-7 18-4-7-7-4z" /></svg>
        </span>
        send-queue
      </button>
      <nav class="no-scrollbar flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto max-md:hidden">
        {pagesFor(auth).map((p, i) => {
          const on = p.tab === tab;
          return (
            <Fragment key={p.tab}>
              {(i === 3 || p.tab === "logs") && <span class="mx-1.5 h-5 w-px flex-none bg-rule" />}
              <button onClick={() => pick(p.tab)} aria-current={on ? "page" : undefined} title={p.label}
                class={`flex min-h-8 flex-none items-center gap-1.5 rounded-lg px-2 text-[13px] transition-colors duration-150 ${on ? "bg-active text-ink" : "text-muted hover:bg-hover hover:text-ink"}`}>
                <svg viewBox="0 0 24 24" class={`size-[15px] flex-none ${on && p.color ? p.color : p.color && (p.tab === "chats" || p.tab === "line") ? p.color + " opacity-80" : ""}`}
                  stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round">{p.icon}</svg>
                <span class={i >= 3 ? "max-2xl:sr-only" : ""}>{p.label}</span>
                {p.tab === "pending" && pending > 0 && (
                  <span class="grid min-w-[18px] place-items-center rounded-full bg-amber px-1 text-[11px] font-bold leading-[18px] text-black">{pending}</span>
                )}
              </button>
            </Fragment>
          );
        })}
      </nav>
      <DemoButton />
      <LangButton />
      <ThemeButton />
      <button onClick={() => setAccount(true)} title={t("{0} · {1} — บัญชี, passkey, ออก", auth.email, roleName(auth.role))}
        class="flex flex-none items-center gap-2 rounded-full py-1 pl-1 pr-3 text-[13px] text-muted hover:bg-hover hover:text-ink max-sm:hidden">
        <span class="grid size-7 place-items-center rounded-full bg-raised text-[12px] font-semibold uppercase text-ink">{auth.email[0]}</span>
        <span class="max-w-[150px] truncate max-xl:hidden">{pvEmail(auth.email)}</span>
        <span class="rounded-full bg-raised px-2 py-px text-[11px]">{roleName(auth.role)}</span>
      </button>
      {account && <Account close={() => setAccount(false)} />}
    </header>
  );
}

// One tap moves to the next theme (auto → dark → light → paper); the icon and the tooltip say which
// is on. The account panel has the same choice as four swatches.
const THEME_ICON: Record<Theme, string> = {
  auto: "M12 3a9 9 0 1 0 0 18V3z M12 3a9 9 0 0 1 0 18",
  dark: "M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z",
  light: "M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8z M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
  paper: "M7 3h7l5 5v13H7z M14 3v5h5 M10 13h6M10 17h4",
};
function ThemeButton() {
  const [cur, setCur] = useState<Theme>(getTheme());
  useEffect(() => {
    const on = () => setCur(getTheme());
    addEventListener("sq-theme", on);
    return () => removeEventListener("sq-theme", on);
  }, []);
  const label = THEMES.find(([k]) => k === cur)![1];
  const next = () => {
    const order = THEMES.map(([k]) => k);
    const n = order[(order.indexOf(cur) + 1) % order.length];
    setTheme(n);
    setCur(n);
  };
  return (
    <button onClick={next} title={t("ธีม: {0} — กดเพื่อเปลี่ยน", label)} aria-label={t("ธีม {0}", label)}
      class="flex flex-none items-center gap-1.5 rounded-full px-2.5 py-1.5 text-[12.5px] text-muted hover:bg-hover hover:text-ink">
      <svg viewBox="0 0 24 24" class="size-[17px]" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d={THEME_ICON[cur]} /></svg>
      <span class="max-lg:hidden">{label}</span>
    </button>
  );
}

// Demo mode for screenshots: made-up names and lines in place of real ones, pictures blurred
// (privacy.ts). On, the button is amber and says so, so a share never starts in the wrong mode.
function DemoButton() {
  return (
    <button type="button" onClick={() => setPrivacy(!privacy)} aria-pressed={privacy}
      title={privacy ? t("โหมดเดโมเปิดอยู่ — กดเพื่อแสดงข้อมูลจริง") : t("โหมดเดโม: แทนชื่อ ข้อความ และรูป ด้วยข้อมูลจำลอง สำหรับแคปจอ")}
      class={`flex flex-none items-center gap-1.5 rounded-full px-2.5 py-1.5 text-[12.5px] ${privacy ? "bg-amber/15 font-semibold text-amber" : "text-muted hover:bg-hover hover:text-ink"}`}>
      <svg viewBox="0 0 24 24" class="size-[17px]" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d={privacy ? "M3 3l18 18 M10.6 6.1A9.9 9.9 0 0 1 12 6c5 0 9 6 9 6a17 17 0 0 1-3.2 3.7 M6.6 6.6C4.3 8.1 3 12 3 12s4 6 9 6a9.5 9.5 0 0 0 4.4-1.1 M9.9 10a3 3 0 0 0 4.2 4.2" : "M3 12s4-6 9-6 9 6 9 6-4 6-9 6-9-6-9-6z M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z"} />
      </svg>
      <span class="max-lg:hidden">{t("เดโม")}</span>
    </button>
  );
}

// TH / EN: the language not in use, as one tap. Switching reloads the page (see i18n.ts).
function LangButton() {
  const other = getLang() === "th" ? "en" : "th";
  return (
    <button type="button" onClick={() => setLang(other)} title={other === "en" ? "Switch to English" : "เปลี่ยนเป็นภาษาไทย"} aria-label={other === "en" ? "English" : "ภาษาไทย"}
      class="flex-none rounded-full px-2.5 py-1.5 text-[12.5px] font-semibold tracking-wide text-muted hover:bg-hover hover:text-ink">
      {other === "en" ? "EN" : "ไทย"}
    </button>
  );
}

// On plain http (the phone's own :8097) the browser forbids passkeys. Instead of hiding them, offer
// the same button pointing at the https address the server is configured for.
function PasskeyElsewhere() {
  const [origin, setOrigin] = useState("");
  useEffect(() => void fetch("/api/passkey/where").then((r) => r.json()).then((j) => setOrigin(j.origin || ""), () => {}), []);
  if (!origin || origin === location.origin) return null;
  return (
    <>
      <a href={origin + "/" + location.hash} class="btn-approve text-center">{t("เข้าด้วย passkey")} ↗</a>
      <p class="text-center text-xs text-muted">{t("passkey ใช้ได้ที่ {0} (ที่อยู่นี้เป็น http)", new URL(origin).host)}</p>
      <p class="flex items-center gap-3 text-xs text-muted before:h-px before:flex-1 before:bg-rule after:h-px after:flex-1 after:bg-rule">{t("หรือใช้รหัสผ่าน")}</p>
    </>
  );
}

function Login({ linkErr }: { linkErr: string }) {
  const [err, setErr] = useState(linkErr);
  useEffect(() => setErr(linkErr), [linkErr]);
  const [busy, setBusy] = useState(false);
  return (
    <form
      class="mx-auto mt-[12vh] grid w-[calc(100%-2rem)] max-w-[360px] gap-3 rounded-2xl border border-rule bg-panel p-6 shadow-[0_24px_60px_-24px_rgba(0,0,0,0.7)]"
      onSubmit={async (e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        setBusy(true);
        try {
          await login(String(d.get("email")), String(d.get("password")));
        } catch (x: any) {
          setErr(x.message);
        }
        setBusy(false);
      }}
    >
      <div class="mb-2 grid justify-items-center gap-2 text-center">
        <span class="grid size-12 place-items-center rounded-2xl bg-amber/15 text-amber">
          <svg viewBox="0 0 24 24" class="size-6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 3 10 14 M21 3l-7 18-4-7-7-4z" /></svg>
        </span>
        <h1 class="text-[19px] font-semibold tracking-tight">send-queue</h1>
        <p class="text-[13px] leading-relaxed text-muted">{t("เข้าระบบด้วยบัญชีผู้ตรวจ")}<br />{t("(superuser หรือ users ที่ role = reviewer)")}</p>
      </div>
      {!passkeySupported() && <PasskeyElsewhere />}
      {passkeySupported() && (
        <>
          <button type="button" disabled={busy} class="btn-approve"
            onClick={async () => {
              setBusy(true);
              setErr("");
              try {
                await loginWithPasskey();
              } catch (x: any) {
                setErr(x.message);
              }
              setBusy(false);
            }}>
            {t("เข้าด้วย passkey")}
          </button>
          <p class="flex items-center gap-3 text-xs text-muted before:h-px before:flex-1 before:bg-rule after:h-px after:flex-1 after:bg-rule">{t("หรือใช้รหัสผ่าน")}</p>
        </>
      )}
      <input name="email" type="email" autocomplete="username" placeholder={t("อีเมล")} required class="field" />
      <input name="password" type="password" autocomplete="current-password" placeholder={t("รหัสผ่าน")} required class="field" />
      <button disabled={busy} class={passkeySupported() ? "btn" : "btn-approve"}>{t("เข้าระบบด้วยรหัสผ่าน")}</button>
      {err && <p role="alert" class="rounded-xl bg-red/10 px-3 py-2 text-[13px] leading-relaxed text-red">{err}</p>}
      <div class="mt-1 flex justify-center"><LangButton /></div>
    </form>
  );
}
