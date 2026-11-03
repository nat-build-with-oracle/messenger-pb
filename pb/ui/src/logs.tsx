// Logs: what this server and the phone did, and whether it worked. Three sources in one timeline:
// Discord forwards (ledger hash + HTTP result), the send-queue's delivery events (claimed / sent /
// failed, with the sender's proof), and the phone's cron runs (GET /api/phone/cronlog: which app it
// brought to the front, and whether it really got there). Success stays quiet; a failure is red and
// says why. A run of identical cron results folds into one row with its count, so 155 failed runs in
// a row read as one loud line instead of a wall.
import { Fragment } from "preact";
import { useMemo, useState } from "preact/hooks";
import { api, getAuth } from "./api";
import { usePoll } from "./hooks";
import { Empty, PaneTitle, Refresh } from "./pane";
import { type Ledger } from "./settings";
import { locale, t } from "./i18n";
import { pvName, pvText } from "./privacy";

type Kind = "discord" | "queue" | "cron";
type CronRun = { at: string; app: string; ok: string; before: string; after: string; front?: string; vpn: string; pb: string; note: string; raw: string };
type Row = {
  id: string; ms: number; kind: Kind; ok: boolean | null; title: string; text: string; status: string;
  hash?: string; http?: number; error?: string; proof?: string; by?: string; rev?: number; thread?: string;
  runs?: CronRun[]; // cron: the runs this row stands for, newest first
};
type Data = { rows: Row[]; cron: { available: boolean; runs: CronRun[] } };
type Filter = "all" | "failed" | Kind;

const f = encodeURIComponent;
const ms = (d: string) => Date.parse(d.includes("T") ? d : d.replace(" ", "T"));
const APP = { line: "LINE", messenger: "Messenger" } as Record<string, string>;

// What the window in front means, in words. NotificationShade = the lock screen or the pulled-down
// shade: the app was launched but never came to the front.
function why(after: string) {
  if (!after || after === "none") return t("ไม่มีหน้าต่างใดอยู่หน้าจอ");
  if (after.startsWith("NotificationShade")) return t("หน้าล็อกหรือแถบแจ้งเตือนบังอยู่");
  if (after.startsWith("NOT on inbox")) return t("ไม่ได้อยู่หน้ากล่องแชท");
  return after.replace(/^[\w.]+\//, "");
}

function cronRow(group: CronRun[]): Row {
  const r = group[0];
  const apps = [...new Set(group.map((g) => APP[g.app] || g.app))].join("/");
  const ok = r.ok === "yes" ? true : r.ok === "no" ? false : null;
  const title =
    r.ok === "skip" ? t("ข้ามรอบ") : r.ok === "note" ? `${APP[r.app] || r.app} · ${r.note}` : t("เปิด {0} ขึ้นหน้าจอ", apps);
  const text =
    r.ok === "yes" ? t("ขึ้นหน้าจอแล้ว") : r.ok === "no" ? t("ไม่ขึ้น · {0}", why(r.after)) : r.ok === "skip" ? t("มีคนใช้โทรศัพท์อยู่") : "";
  return {
    id: "cron:" + r.raw, ms: ms(r.at), kind: "cron", ok, title, runs: group,
    text,
    status: ok === false ? "failed" : ok ? "ok" : r.ok,
  };
}

// Consecutive runs with the same outcome fold together (LINE and Messenger alternate, so the key is
// the outcome, not the app).
function foldCron(runs: CronRun[]): Row[] {
  const out: Row[] = [];
  let group: CronRun[] = [];
  const key = (r: CronRun) => `${r.ok}|${r.ok === "no" ? why(r.after) : ""}`;
  for (const r of runs) {
    if (group.length && key(group[0]) !== key(r)) out.push(cronRow(group)), (group = []);
    group.push(r);
  }
  if (group.length) out.push(cronRow(group));
  return out;
}

async function load(): Promise<Data> {
  const rows: Row[] = [];
  const su = getAuth()?.col === "_superusers";
  const [log, evs, cron] = await Promise.all([
    su ? api(`/api/collections/forward_log/records?perPage=100&sort=-created&expand=forward`).then((j) => j.items as (Ledger & { expand?: any })[]) : Promise.resolve([]),
    api(`/api/collections/send_events/records?perPage=100&sort=-created&expand=send&filter=${f('kind="sending" || kind="sent" || kind="failed"')}`).then((j) => j.items as any[]),
    api(`/api/phone/cronlog?limit=300`).catch(() => ({ available: false, runs: [] })) as Promise<Data["cron"]>,
  ]);
  // PocketBase refuses a filter of 100 OR-ed ids (400 on the phone, 2026-10-29): ask in groups of 20,
  // and keep the log even if a group fails (the row then shows its message id).
  const mids = [...new Set(log.map((l) => l.mid))];
  const groups: string[][] = [];
  for (let i = 0; i < mids.length; i += 20) groups.push(mids.slice(i, i + 20));
  const msgs = (
    await Promise.all(
      groups.map((g) =>
        api(`/api/collections/messages/records?perPage=20&fields=mid,sender_name,text,thread_key&filter=${f(g.map((m) => `mid="${m.replace(/"/g, "")}"`).join(" || "))}`)
          .then((j) => j.items as any[], () => [] as any[]),
      ),
    )
  ).flat();
  const byMid = new Map(msgs.map((m) => [m.mid, m]));
  for (const l of log) {
    const m = byMid.get(l.mid);
    rows.push({ id: l.id, ms: ms(l.created), kind: "discord", status: l.status, ok: l.status === "sent" ? true : l.status === "failed" ? false : null,
      title: l.expand?.forward?.name || "Discord", text: m ? `${m.sender_name}: ${m.text}` : l.mid, hash: l.hash, http: l.http, error: l.error,
      thread: l.expand?.forward?.thread_key || m?.thread_key });
  }
  for (const e of evs) {
    rows.push({ id: e.id, ms: ms(e.created), kind: "queue", status: e.kind, ok: e.kind === "sent" ? true : e.kind === "failed" ? false : null,
      title: e.expand?.send?.chat || t("คิว"), text: e.expand?.send?.message || "", proof: e.comment, by: e.by, rev: e.rev,
      hash: (e.comment || "").match(/hash ([0-9a-f]{8,})/)?.[1] });
  }
  rows.push(...foldCron(cron.runs || []));
  return { rows: rows.sort((a, b) => b.ms - a.ms), cron: { available: !!cron.available, runs: cron.runs || [] } };
}

const time = (v: number) => new Date(v).toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" });
const full = (v: number) => new Date(v).toLocaleString(locale(), { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
function day(v: number) {
  const d = new Date(v), now = new Date(), y = new Date(+now - 864e5);
  if (d.toDateString() === now.toDateString()) return t("วันนี้");
  if (d.toDateString() === y.toDateString()) return t("เมื่อวาน");
  return d.toLocaleDateString(locale(), { weekday: "short", day: "numeric", month: "short" });
}

// Drawn icons, one stroke weight, same family as the rail.
const PATH: Record<Kind, string> = {
  discord: "M5 5h14a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1h-7l-4 3v-3H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1z M9 10.5h.01M15 10.5h.01",
  queue: "M21 3 10 14 M21 3l-7 18-4-7-7-4z",
  cron: "M12 7v5l3 2 M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z",
};
const KIND_NAME: Record<Kind, string> = { discord: t("ส่งต่อไป Discord"), queue: t("ส่งข้อความผ่านคิว"), cron: t("งานตั้งเวลาบนโทรศัพท์") };
function Icon({ kind, ok }: { kind: Kind; ok: boolean | null }) {
  const tone = ok === false ? "text-red" : kind === "discord" ? "text-discord-ink" : kind === "queue" ? "text-blue" : "text-muted";
  return (
    <svg viewBox="0 0 24 24" class={`size-[18px] flex-none ${tone}`} fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-label={KIND_NAME[kind]}>
      <path d={PATH[kind]} />
    </svg>
  );
}

// The phone at a glance, from the newest cron run: is the job working, is the VPN up, is this
// server reachable on it. Only lines with something to say are shown.
function Health({ data }: { data: Data }) {
  const runs = data.cron.runs, last = runs[0];
  const day1 = Date.now() - 864e5;
  const fwd = data.rows.filter((r) => r.kind === "discord" && r.ms > day1);
  const fwdFail = fwd.filter((r) => r.ok === false).length;
  // A failure streak runs back until the last success; skipped runs inside it do not end it.
  let streak = 0, since: CronRun | null = null;
  for (const r of runs) {
    if (r.ok === "yes") break;
    if (r.ok === "no") (streak++, (since = r));
  }
  const lines: [boolean | null, string, string][] = [];
  if (!data.cron.available) lines.push([null, t("งานตั้งเวลา"), t("เครื่องนี้ไม่มีบันทึกของ cron")]);
  else if (last) {
    lines.push(streak > 1
      ? [false, t("งานตั้งเวลา"), t("ไม่สำเร็จ {0} รอบติดกัน ตั้งแต่ {1} · {2}", streak, full(ms(since!.at)), why(last.after))]
      : [last.ok !== "no", t("งานตั้งเวลา"), t("ล่าสุด {0} · {1}", time(ms(last.at)), cronRow([last]).text)]);
    if (last.vpn) lines.push([last.vpn === "up", "VPN", last.vpn === "up" ? t("ต่ออยู่ (Tailscale)") : t("หลุด: โทรศัพท์ไม่มีที่อยู่ 100.x")]);
    if (last.pb) lines.push([last.pb === "up", t("เซิร์ฟเวอร์"), last.pb === "up" ? t("ตอบอยู่บนโทรศัพท์") : t("ไม่ตอบที่พอร์ต 8097")]);
  }
  if (getAuth()?.col === "_superusers") lines.push([fwdFail ? false : true, t("Discord 24 ชม."), fwd.length ? (fwdFail ? t("ส่ง {0} · ล้มเหลว {1}", fwd.length - fwdFail, fwdFail) : t("ส่ง {0}", fwd.length)) : t("ยังไม่มีการส่ง")]);
  return (
    <dl class="mx-4 mb-3 grid grid-cols-[auto_auto_1fr] items-baseline gap-x-2.5 gap-y-1.5 rounded-xl bg-sunk px-3.5 py-3 text-[13px]">
      {lines.map(([ok, label, text]) => (
        <Fragment key={label}>
          <span class={`size-2 translate-y-[-1px] rounded-full ${ok === false ? "bg-red" : ok ? "bg-green" : "bg-muted/60"}`} aria-hidden />
          <dt class="text-muted">{label}</dt>
          <dd class={ok === false ? "text-red" : ""}>{text}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

const FILTERS: [Filter, string][] = [["all", t("ทั้งหมด")], ["failed", t("ล้มเหลว")], ["cron", "cron"], ["discord", "Discord"], ["queue", t("คิว")]];

export function Logs() {
  const { data, error, loading, reload } = usePoll(load, [], 10000);
  const [open, setOpen] = useState("");
  const [only, setOnly] = useState<Filter>("all");
  const rows = useMemo(
    () => (data?.rows || []).filter((r) => (only === "all" ? true : only === "failed" ? r.ok === false : r.kind === only)),
    [data, only],
  );
  const failed = data?.rows.filter((r) => r.ok === false).length || 0;
  const row = rows.find((r) => r.id === open) || (open ? data?.rows.find((r) => r.id === open) : rows[0]);
  let lastDay = "";
  return (
    <div class="grid min-h-0 flex-1 gap-3 p-3 md:grid-cols-[minmax(320px,420px)_1fr] max-md:p-0">
      <section class={`flex min-h-0 flex-col overflow-hidden rounded-xl bg-panel max-md:rounded-none ${open ? "max-md:hidden" : ""}`}>
        <PaneTitle title={t("บันทึก")} home={() => (setOpen(""), setOnly("all"))} refresh={reload} loading={loading} />
        {data && <Health data={data} />}
        <div class="mx-4 mb-2 flex flex-wrap gap-1.5" role="group" aria-label={t("ตัวกรอง")}>
          {FILTERS.map(([v, label]) => (
            <button key={v} onClick={() => setOnly(v)} aria-pressed={only === v}
              class={`rounded-full px-3 py-1 text-[13px] focus-visible:outline-2 focus-visible:outline-mine ${only === v ? (v === "failed" ? "bg-red/15 text-red" : "bg-select text-select-ink") : "bg-raised text-muted hover:text-ink"}`}>
              {label}{v === "failed" && failed ? <span class="ml-1 tabular-nums">{failed}</span> : null}
            </button>
          ))}
        </div>
        <ul class="flex-1 overflow-y-auto px-2 pb-2">
          {!data && !error && Array.from({ length: 6 }, (_, i) => (
            <li key={i} class="mx-2 my-2 h-11 animate-pulse rounded-lg bg-raised" />
          ))}
          {rows.map((r) => {
            const d = day(r.ms), head = d !== lastDay ? ((lastDay = d), d) : "";
            const on = r.id === row?.id;
            return (
              <li key={r.id}>
                {head && <div class="sticky top-0 z-10 bg-panel px-2.5 pb-1 pt-3 text-[12px] font-semibold text-muted">{head}</div>}
                <button onClick={() => setOpen(r.id)} aria-current={on || undefined}
                  class={`grid w-full grid-cols-[42px_18px_1fr] items-start gap-x-2.5 rounded-lg px-2.5 py-2 text-left focus-visible:outline-2 focus-visible:outline-mine ${on ? "bg-select" : "hover:bg-hover"}`}>
                  <span class="pt-px text-[12.5px] tabular-nums text-muted">{time(r.ms)}</span>
                  <Icon kind={r.kind} ok={r.ok} />
                  <span class="min-w-0">
                    <span class="flex items-center gap-2">
                      <span class={`truncate text-[14px] ${r.ok === false ? "font-semibold" : ""}`}>{r.kind === "cron" ? r.title : pvName(r.title)}</span>
                      {r.runs && r.runs.length > 1 && (
                        <span class={`flex-none rounded-full px-1.5 text-[11px] font-semibold tabular-nums ${r.ok === false ? "bg-red/15 text-red" : "bg-raised text-muted"}`}>×{r.runs.length}</span>
                      )}
                    </span>
                    <span class={`mt-0.5 block truncate text-[12.5px] ${r.ok === false ? "text-red" : "text-muted"}`}>
                      {r.kind === "discord" && r.ok === false ? `HTTP ${r.http || "—"} · ${r.error || pvText(r.text)}` : r.kind === "cron" ? r.text : pvText(r.text)}
                    </span>
                  </span>
                </button>
              </li>
            );
          })}
          {data && !rows.length && (
            <li><Empty>{only === "failed" ? t("ไม่มีอะไรล้มเหลว") : only === "cron" ? t("ยังไม่มีบันทึกจากงานตั้งเวลา") : t("ยังไม่มีอะไรถูกส่งออก")}</Empty></li>
          )}
          {error && <li class="py-12 text-center text-red">{error}</li>}
        </ul>
      </section>
      <section class={`flex min-h-0 flex-col overflow-hidden rounded-xl bg-panel max-md:rounded-none ${open ? "" : "max-md:hidden"}`}>
        {row ? <Detail row={row} back={() => setOpen("")} reload={reload} loading={loading} /> : <Empty>{t("เลือกรายการทางซ้าย")}</Empty>}
      </section>
    </div>
  );
}

function Detail({ row, back, reload, loading }: { row: Row; back: () => void; reload: () => void; loading: boolean }) {
  const r = row.runs?.[0], first = row.runs?.[row.runs.length - 1];
  const okWord = row.ok === false ? t("ไม่สำเร็จ") : row.ok ? t("สำเร็จ") : row.status;
  return (
    <div class="flex min-h-0 flex-1 flex-col">
      <div class="flex items-center gap-2.5 border-b border-rule px-4 py-2.5">
        <button onClick={back} class="text-muted md:hidden" aria-label={t("กลับ")}>←</button>
        <Icon kind={row.kind} ok={row.ok} />
        <b class="truncate">{row.kind === "cron" ? row.title : pvName(row.title)}</b>
        <span class={`rounded-full px-2 py-px text-xs font-semibold ${row.ok === false ? "bg-red/15 text-red" : row.ok ? "bg-green/15 text-green" : "bg-amber/15 text-amber"}`}>{okWord}</span>
        <span class="ml-auto"><Refresh onClick={reload} loading={loading} /></span>
      </div>
      <div class="flex-1 overflow-y-auto">
        <dl class="grid grid-cols-[132px_1fr] content-start gap-x-4 gap-y-2.5 p-4 text-[13.5px]">
          <dt class="text-muted">{t("ประเภท")}</dt><dd>{KIND_NAME[row.kind]}</dd>
          {r && first && row.runs!.length > 1
            ? (<><dt class="text-muted">{t("ช่วงเวลา")}</dt><dd>{full(ms(first.at))} – {full(ms(r.at))} <span class="text-muted">{t("({0} รอบ)", row.runs!.length)}</span></dd></>)
            : (<><dt class="text-muted">{t("เวลา")}</dt><dd>{full(row.ms)}</dd></>)}
          {r && (<>
            <dt class="text-muted">{t("ผล")}</dt><dd class={row.ok === false ? "text-red" : ""}>{row.text}{row.runs!.length > 1 ? t(" · {0} รอบติดกัน", row.runs!.length) : ""}</dd>
            {r.before && (<><dt class="text-muted">{t("หน้าจอก่อนรัน")}</dt><dd><code class="break-all text-[12.5px]">{r.before}</code></dd></>)}
            {r.front && (<><dt class="text-muted">{t("แอปหน้าสุด")}</dt><dd><code class="break-all text-[12.5px]">{r.front}</code></dd></>)}
            {r.after && (<><dt class="text-muted">{t("หน้าจอหลังรัน")}</dt><dd><code class="break-all text-[12.5px]">{r.after}</code></dd></>)}
            {r.vpn && (<><dt class="text-muted">VPN</dt><dd class={r.vpn === "up" ? "" : "text-red"}>{r.vpn === "up" ? t("ต่ออยู่") : t("หลุด")}</dd></>)}
            {r.pb && (<><dt class="text-muted">{t("เซิร์ฟเวอร์")}</dt><dd class={r.pb === "up" ? "" : "text-red"}>{r.pb === "up" ? t("ตอบอยู่") : t("ไม่ตอบ")}</dd></>)}
          </>)}
          {row.http !== undefined && (<><dt class="text-muted">{t("ผล HTTP")}</dt><dd>{row.http || t("ไม่ได้คำตอบ")}</dd></>)}
          {row.hash && (<><dt class="text-muted">hash</dt><dd><code class="break-all">{row.hash}</code></dd></>)}
          {row.rev !== undefined && (<><dt class="text-muted">rev</dt><dd>{row.rev}</dd></>)}
          {row.by && (<><dt class="text-muted">{t("โดย")}</dt><dd>{row.by}</dd></>)}
          {row.thread && (<><dt class="text-muted">{t("แชท")}</dt><dd class="flex flex-wrap gap-x-3">
            <a class="text-select-ink underline-offset-2 hover:underline" href={`#c=${row.thread}`}>{t("เปิดในแอปนี้")}</a>
            <a class="text-select-ink underline-offset-2 hover:underline" href={`https://www.messenger.com/t/${row.thread}`} target="_blank" rel="noopener">{t("เปิดใน Messenger ↗")}</a>
          </dd></>)}
          {row.kind !== "cron" && (<><dt class="text-muted">{t("ข้อความ")}</dt><dd class="whitespace-pre-wrap break-words">{pvText(row.text)}</dd></>)}
          {row.proof && (<><dt class="text-muted">{t("หลักฐาน")}</dt><dd class="whitespace-pre-wrap break-words">{row.proof}</dd></>)}
          {row.error && (<><dt class="text-muted">{t("ข้อผิดพลาด")}</dt><dd class="whitespace-pre-wrap break-words text-red">{row.error}</dd></>)}
        </dl>
        {row.runs && (
          <div class="px-4 pb-4">
            <h3 class="mb-2 text-[13px] font-semibold text-muted">{t("บรรทัดจริงจาก rotate.log")}{row.runs.length > 40 ? t(" (40 จาก {0})", row.runs.length) : ""}</h3>
            <pre class="overflow-x-auto rounded-lg bg-sunk p-3 text-[12px] leading-relaxed text-muted">{row.runs.slice(0, 40).map((x) => x.raw).join("\n")}</pre>
          </div>
        )}
      </div>
    </div>
  );
}
