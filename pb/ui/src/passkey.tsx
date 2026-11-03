// The account panel: who is signed in, their passkeys (add / remove), sign out.
// Passkeys are for a reviewer or superuser only (passkey.go); the password keeps working.
import { useEffect, useState } from "preact/hooks";
import { addPasskey, deletePasskey, getAuth, listPasskeys, logout, passkeySupported, type Passkey, openAdmin } from "./api";
import { pbDate } from "./time";
import { getTheme, setTheme, THEMES, type Theme } from "./theme";
import { t } from "./i18n";
import { pvEmail } from "./privacy";

const guessName = () => {
  const ua = navigator.userAgent;
  if (/iPhone/.test(ua)) return "iPhone Face ID";
  if (/iPad/.test(ua)) return "iPad";
  if (/Macintosh/.test(ua)) return "Mac Touch ID";
  if (/Android/.test(ua)) return "Android";
  return "passkey";
};
// A second passkey from the same kind of device gets a number, so two rows never read the same.
const freshName = (taken: string[]) => {
  const base = guessName();
  if (!taken.includes(base)) return base;
  let n = 2;
  while (taken.includes(`${base} ${n}`)) n++;
  return `${base} ${n}`;
};
const KIND = (name: string) =>
  /iphone|ipad|face/i.test(name) ? "M8 4H6a2 2 0 0 0-2 2v2M16 4h2a2 2 0 0 1 2 2v2M8 20H6a2 2 0 0 1-2-2v-2M16 20h2a2 2 0 0 0 2-2v-2M9 10v1M15 10v1M12 10v3h-1M9.5 16c1.5 1 3.5 1 5 0"
  : /key|usb|yubi/i.test(name) ? "M14 10a4 4 0 1 1-1.2-2.9L21 7v3h-2v2h-2l-2.3 2.3A4 4 0 0 1 14 10z M9 10h.01"
  : "M12 11v3m-3.5-5.5A4.5 4.5 0 0 1 16.5 11v1.5M7.5 11a4.5 4.5 0 0 0 .2 1.4M12 7a6 6 0 0 0-6 6v1M18 13.5c0 2-.4 3.8-1.2 5.3M9.5 14.5c0 2-.5 3.8-1.4 5.3M14.5 13c0 2.8-.6 5.3-1.8 7.2";
const Svg = ({ d, cls = "size-[18px]" }: { d: string; cls?: string }) => (
  <svg viewBox="0 0 24 24" class={`${cls} flex-none`} fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d={d} /></svg>
);

const SWATCH: Record<Theme, string> = {
  auto: "linear-gradient(135deg, #13120f 50%, #f4f3f0 50%)",
  dark: "#13120f",
  light: "#f4f3f0",
  paper: "#efe7d8",
};

export function Account({ close }: { close: () => void }) {
  const auth = getAuth()!;
  const canPasskey = auth.role === "reviewer";
  const [items, setItems] = useState<Passkey[] | null>(null);
  const [configured, setConfigured] = useState(true);
  const [name, setName] = useState(guessName());
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const [theme, setThemeState] = useState<Theme>(getTheme());
  const load = () =>
    listPasskeys().then(
      (r) => (setItems(r.items), setConfigured(r.configured), setName(freshName(r.items.map((i) => i.name)))),
      (x) => setMsg(x.message),
    );
  useEffect(() => void (canPasskey && load()), []);
  useEffect(() => {
    const esc = (e: KeyboardEvent) => e.key === "Escape" && close();
    addEventListener("keydown", esc);
    return () => removeEventListener("keydown", esc);
  }, []);

  const add = async () => {
    setBusy(true);
    setMsg("");
    try {
      if (await addPasskey(name.trim() || guessName())) {
        setMsg(t("เพิ่ม passkey แล้ว — ครั้งหน้ากด “เข้าด้วย passkey” ได้เลย"));
        await load();
      } else setMsg(t("ยังไม่ได้เพิ่ม: ยกเลิก หรือยืนยันตัวตน (นิ้ว/หน้า/PIN) ไม่ผ่าน"));
    } catch (x: any) {
      setMsg(x.message);
    }
    setBusy(false);
  };

  const why = !configured
    ? t("เซิร์ฟเวอร์นี้ยังไม่ได้ตั้งค่า passkey")
    : !passkeySupported()
      ? t("passkey ใช้ได้เฉพาะหน้าที่เปิดผ่าน https (ที่อยู่ของ tailscale serve)")
      : "";

  const roleName = ({ reviewer: t("ผู้ตรวจ"), viewer: t("ผู้อ่าน") } as Record<string, string>)[auth.role] || auth.role;
  return (
    <div class="fixed inset-0 z-40 grid place-items-start justify-center bg-black/55 px-4 pt-[10vh] backdrop-blur-[2px]" onClick={(e) => e.target === e.currentTarget && close()}>
      <section role="dialog" aria-modal="true" aria-label={t("บัญชี")}
        class="grid w-full max-w-[440px] gap-5 rounded-2xl border border-rule bg-panel p-5 text-[13.5px] shadow-[0_24px_60px_-20px_rgba(0,0,0,0.7)]">
        <div class="flex items-center gap-3">
          <span class="grid size-10 flex-none place-items-center rounded-full bg-amber/15 text-[15px] font-semibold uppercase text-amber">{auth.email[0]}</span>
          <span class="min-w-0 flex-1">
            <b class="block truncate text-[15px]">{pvEmail(auth.email)}</b>
            <span class="text-xs text-muted">{roleName}{auth.col === "_superusers" ? t(" · ผู้ดูแลระบบ") : ""}</span>
          </span>
          <button onClick={close} aria-label={t("ปิด")} title={t("ปิด (Esc)")} class="grid size-9 place-items-center rounded-full text-muted hover:bg-raised hover:text-ink">
            <Svg d="M6 6l12 12M18 6 6 18" />
          </button>
        </div>

        <div class="grid gap-2">
          <h3 class="text-[14px] font-semibold">{t("ธีม")}</h3>
          <div role="radiogroup" aria-label={t("ธีม")} class="grid grid-cols-4 gap-1.5">
            {THEMES.map(([t, label]) => (
              <button key={t} role="radio" aria-checked={theme === t} onClick={() => (setTheme(t), setThemeState(t))}
                class={`grid justify-items-center gap-1.5 rounded-xl border px-2 py-2.5 text-[12.5px] transition-colors duration-150 ${theme === t ? "border-amber/60 bg-raised text-ink" : "border-rule text-muted hover:bg-hover hover:text-ink"}`}>
                <span class="size-6 rounded-full border border-rule" style={{ background: SWATCH[t] }} />
                {label}
              </button>
            ))}
          </div>
        </div>

        {canPasskey && (
          <div class="grid gap-3">
            <div>
              <h3 class="text-[14px] font-semibold">Passkey{items?.length ? <span class="ml-1.5 font-normal text-muted">{items.length}</span> : null}</h3>
              <p class="mt-0.5 text-[12.5px] leading-relaxed text-muted">{t("เข้าระบบด้วย Touch ID, Face ID หรือกุญแจ USB แทนการพิมพ์รหัสผ่าน · รหัสผ่านยังใช้ได้")}</p>
            </div>
            {items === null ? (
              <div class="h-14 animate-pulse rounded-xl bg-card" />
            ) : items.length === 0 ? (
              <p class="rounded-xl border border-dashed border-rule px-3 py-4 text-center text-muted">{t("ยังไม่มี passkey · เพิ่มอันแรกด้านล่าง")}</p>
            ) : (
              <ul class="grid gap-1.5">
                {items.map((p) => (
                  <li key={p.id} class="group flex items-center gap-3 rounded-xl bg-card px-3 py-2.5">
                    <span class="grid size-9 flex-none place-items-center rounded-lg bg-raised text-amber"><Svg d={KIND(p.name)} /></span>
                    <span class="min-w-0 flex-1">
                      <span class="block truncate font-medium">{p.name}</span>
                      <span class="text-xs text-muted">
                        {p.last_used ? t("ใช้ล่าสุด {0}", pbDate(p.last_used)) : t("ยังไม่เคยใช้")} · {t("เพิ่ม {0}", pbDate(p.created))}
                      </span>
                    </span>
                    <button class="grid size-8 place-items-center rounded-full text-muted hover:bg-red/15 hover:text-red" disabled={busy}
                      aria-label={t("ลบ {0}", p.name)} title={t("ลบ passkey นี้")}
                      onClick={async () => {
                        if (!confirm(t("ลบ passkey “{0}”? อุปกรณ์นี้จะเข้าระบบด้วย passkey ไม่ได้อีก", p.name))) return;
                        try {
                          await deletePasskey(p.id);
                          await load();
                        } catch (x: any) {
                          setMsg(x.message);
                        }
                      }}>
                      <Svg d="M5 7h14M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3" cls="size-4" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
            {why ? (
              <p class="rounded-xl bg-amber/10 px-3 py-2 text-xs text-amber">{why}</p>
            ) : (
              <form class="flex gap-2" onSubmit={(e) => (e.preventDefault(), add())}>
                <input value={name} onInput={(e) => setName(e.currentTarget.value)} maxLength={100} aria-label={t("ชื่อ passkey")} placeholder={t("ชื่ออุปกรณ์")} class="field" />
                <button disabled={busy} class="btn-approve flex-none">{busy ? t("รอยืนยัน…") : t("เพิ่ม passkey")}</button>
              </form>
            )}
          </div>
        )}

        {msg && <p class="rounded-xl bg-card px-3 py-2 text-[13px]">{msg}</p>}
        <div class="flex items-center gap-2 border-t border-rule pt-4">
          <button onClick={() => confirm(t("ออกจากระบบ {0}?", auth.email)) && logout()} class="btn-quiet hover:text-red">{t("ออกจากระบบ")}</button>
          {auth.col === "_superusers" && (
            <a href="/_/" target="_blank" rel="noopener" onClick={(e) => (e.preventDefault(), openAdmin())} class="btn-quiet ml-auto">PocketBase admin ↗</a>
          )}
        </div>
      </section>
    </div>
  );
}
