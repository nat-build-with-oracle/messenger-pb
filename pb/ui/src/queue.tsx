// The send-queue: drafts waiting for review, with approve / reject / comment / archive.
// Approving sends the revision number the reviewer is looking at; the server answers 409
// if the agent revised in the meantime, instead of approving text nobody read.
import { useState } from "preact/hooks";
import { Empty, ICON_CHAT, ICON_LIST, PaneTitle, Refresh, Resting, TwoPane } from "./pane";
import { api, fileToken, getAuth, withToken } from "./api";
import { usePoll } from "./hooks";
import { pbDate } from "./time";
import { t } from "./i18n";
import { pvName, pvText } from "./privacy";

export const QUEUE_TABS: [string, string][] = [
  ["pending", t("รอตรวจ")], ["approved", t("อนุมัติแล้ว")], ["done", t("ส่งแล้ว")],
  ["rejected", t("ไม่อนุมัติ")], ["all", t("ทั้งหมด")], ["archived", t("เก็บแล้ว")],
];
const FILTER: Record<string, string> = {
  pending: 'archived=false && status="pending"',
  approved: 'archived=false && status="approved"',
  done: 'archived=false && (status="sent" || status="failed")',
  rejected: 'archived=false && status="rejected"',
  all: "archived=false",
  archived: "archived=true",
};
const STATUS: Record<string, [string, string]> = {
  pending: [t("รอตรวจ"), "bg-amber/15 text-amber"],
  approved: [t("อนุมัติแล้ว"), "bg-green/15 text-green"],
  rejected: [t("ไม่อนุมัติ"), "bg-red/15 text-red"],
  sent: [t("ส่งแล้ว"), "bg-blue/15 text-blue"],
  failed: [t("ส่งไม่สำเร็จ"), "bg-red/15 text-red"],
};
const KIND: Record<string, string> = {
  queued: t("เข้าคิว"), revised: t("แก้ไขข้อความ"), comment: t("agent เขียน"), feedback: t("ความเห็น"), approved: t("อนุมัติ"),
  rejected: t("ไม่อนุมัติ"), sent: t("ส่งแล้ว"), failed: t("ส่งไม่สำเร็จ"), notified: t("แจ้ง agent"), notify_failed: t("แจ้ง agent ไม่สำเร็จ"),
};

type Send = {
  id: string; status: string; chat: string; platform: string; rev: number; batch?: string; part?: string;
  created: string; updated: string; note?: string; message: string; source_note?: string; source_url?: string;
  proof?: string; proof_img?: string; sent_at?: string; archived: boolean;
};
type Ev = { id: string; send: string; kind: string; rev: number; by: string; created: string; comment?: string; body?: string };

const q = (f: string) => encodeURIComponent(f);

export function usePendingCount() {
  const { data } = usePoll(() => api(`/api/collections/sends/records?perPage=1&fields=id&filter=${q(FILTER.pending)}`).then((r) => r.totalItems as number), []);
  return data || 0;
}

export function Queue({ tab, hash, pick }: { tab: string; hash: string; pick: (t: string) => void }) {
  const { data, error, loading, reload } = usePoll(async () => {
    const sends = (await api(`/api/collections/sends/records?perPage=200&sort=-created&filter=${q(FILTER[tab])}`)).items as Send[];
    const evs = sends.length
      ? ((await api(`/api/collections/send_events/records?perPage=500&sort=created&filter=${q(sends.map((s) => `send="${s.id}"`).join(" || "))}`)).items as Ev[])
      : [];
    const tok = sends.some((s) => s.proof_img) ? await fileToken() : "";
    return { sends, evs, tok };
  }, [tab]);
  // #<send id> opens that draft (the link the agent prints); otherwise the newest one.
  const want = hash.length > 1 && !hash.includes("=") ? hash.slice(1) : "";
  const open = data?.sends.find((s) => s.id === want) || (want ? undefined : data?.sends[0]);
  const title = QUEUE_TABS.find(([k]) => k === tab)?.[1] || t("คิว");

  return (
    <TwoPane
      open={!!want}
      list={
        <>
          <PaneTitle title={title} home={() => pick("pending")} refresh={reload} loading={loading} />
          <ul class="flex-1 overflow-y-auto px-2 pb-2">
            {data?.sends.map((s) => {
              const [label, cls] = STATUS[s.status] || [s.status, ""];
              return (
                <li key={s.id}>
                  <a href={`#${s.id}`} class={`block rounded-lg p-2.5 ${s.id === open?.id ? "bg-select" : "hover:bg-hover"}`}>
                    <div class="flex items-center gap-2">
                      <span class={`flex-none rounded-full px-2 py-px text-[11px] font-semibold ${cls}`}>{label}</span>
                      <span class="truncate text-[15px] font-semibold">{pvName(s.chat)}</span>
                      <span class="ml-auto flex-none text-xs text-muted">{pbDate(s.created)}</span>
                    </div>
                    <div class="mt-1 truncate text-[13px] text-muted">{pvText(s.message)}</div>
                  </a>
                </li>
              );
            })}
            {data && !data.sends.length && <li><Empty>{getAuth()?.role ? t("ไม่มีรายการ") : t("บัญชีนี้ไม่มีสิทธิ์ดูคิว (ต้องเป็น superuser หรือ role = reviewer/agent)")}</Empty></li>}
            {error && <li class="py-12 text-center text-red">{error}</li>}
          </ul>
        </>
      }
      detail={
        open ? (
          <div class="flex min-h-0 flex-1 flex-col">
            <div class="flex items-center gap-2.5 border-b border-rule px-4 py-2.5">
              <a href="#" class="text-muted md:hidden" aria-label={t("กลับ")}>←</a>
              <b class="truncate text-base">{open.chat}</b>
              <span class="text-[12.5px] text-muted">{open.platform} · rev {open.rev}</span>
              <span class="ml-auto"><Refresh onClick={reload} loading={loading} /></span>
            </div>
            <div class="flex-1 overflow-y-auto px-4 pb-6">
              <Card key={open.id} s={open} evs={data!.evs.filter((e) => e.send === open.id)} tok={data!.tok} done={reload} />
            </div>
          </div>
        ) : (
          data && want ? <Empty>{t("ไม่พบรายการนี้ในหน้านี้ — ลองหน้า ทั้งหมด")}</Empty> : <Resting icon={ICON_LIST} title={t("เลือกรายการทางซ้าย")} hint={t("ร่างข้อความจาก agent รอคุณอ่าน แก้ หรืออนุมัติก่อนส่ง")} />
        )
      }
    />
  );
}

function Card({ s, evs, tok, done }: { s: Send; evs: Ev[]; tok: string; done: () => void }) {
  const reviewer = getAuth()?.role === "reviewer";
  const [busy, setBusy] = useState(false);
  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
      done();
    } catch (e: any) {
      alert(e.message);
    }
    setBusy(false);
  };
  const url = /^https?:\/\//i.test(s.source_url || "") ? s.source_url : "";
  const [label, cls] = STATUS[s.status] || [s.status, ""];
  return (
    <article id={s.id} class="my-3.5 rounded-xl border border-rule bg-card px-4 py-3.5">
      <div class="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 text-[13px] text-muted">
        <span class={`rounded-full px-2.5 py-px text-xs font-semibold ${cls}`}>{label}</span>
        <span class="text-base font-semibold text-ink">{pvName(s.chat)}</span>
        <span>{s.platform}</span>
        <span>rev {s.rev}</span>
        {s.batch && <span>{s.batch}{s.part ? " · " + s.part : ""}</span>}
        <span>{pbDate(s.created)}</span>
      </div>
      {s.note && <p class="my-1.5 whitespace-pre-wrap text-[13.5px] text-muted">{s.note}</p>}
      <div class="my-2.5 whitespace-pre-wrap break-words rounded-lg border border-rule bg-sunk px-3.5 py-3 text-[15.5px]">{pvText(s.message)}</div>
      {(s.source_note || url) && (
        <p class="my-1.5 text-[13.5px] text-muted">
          <b class="font-semibold text-ink">{t("ที่มา")}</b> {s.source_note}{" "}
          {url && <a href={url} target="_blank" rel="noopener noreferrer" class="text-blue">{t("เปิด")}</a>}
        </p>
      )}
      {s.proof && (
        <div class="my-1.5 text-[13.5px] text-muted">
          <b class="font-semibold text-ink">{t("หลักฐาน")}</b> {s.proof}{s.sent_at ? " · " + pbDate(s.sent_at) : ""}
          {s.proof_img && tok && (
            <img src={withToken(`/api/files/sends/${s.id}/${encodeURIComponent(s.proof_img)}`, tok)} alt={t("ภาพหลักฐานว่าข้อความถูกส่งแล้ว")} class="mt-2 max-w-full rounded-lg border border-rule" />
          )}
        </div>
      )}
      {reviewer && s.status === "pending" && (
        <div class="mt-2.5 flex flex-wrap items-center gap-2.5">
          <button
            disabled={busy}
            class="btn-approve"
            onClick={() => confirm(t("อนุมัติ rev {0}? agent จะส่งข้อความนี้ตามตัวอักษรทุกตัว", s.rev)) && act(() => api(`/api/sq/sends/${s.id}/approve`, { method: "POST", json: { rev: s.rev } }))}
          >
            {t("อนุมัติ rev {0}", s.rev)}
          </button>
          <Fold label={t("ไม่อนุมัติ…")}>
            <OneLine placeholder={t("เหตุผล (ถ้ามี)")} button={t("ยืนยันไม่อนุมัติ")} busy={busy} allowEmpty
              send={(reason) => act(() => api(`/api/sq/sends/${s.id}/reject`, { method: "POST", json: { reason, rev: s.rev } }))} />
          </Fold>
        </div>
      )}
      <Fold label={reviewer ? t("บอก agent ให้แก้…") : t("เขียนถึงผู้ตรวจ…")}>
        <OneLine multiline placeholder={reviewer ? t("เช่น สั้นลงหน่อย / ไม่ต้องขอโทษ") : ""} button={t("ส่งความเห็น")} busy={busy}
          send={(comment) => act(() => api(`/api/sq/sends/${s.id}/comment`, { method: "POST", json: { comment } }))} />
      </Fold>
      <Fold label={t("ประวัติ ({0})", evs.length)}>
        <ul class="mt-1.5 text-[13px] text-muted">
          {evs.map((ev) => (
            <li key={ev.id} class="border-t border-dashed border-rule py-1.5">
              <b class="font-semibold text-ink">{KIND[ev.kind] || ev.kind}</b> · rev {ev.rev} · {ev.by} · {pbDate(ev.created)}
              {ev.comment && <div class="my-1 whitespace-pre-wrap">{ev.comment}</div>}
              {ev.kind === "revised" && ev.body && (
                <Fold label={t("ข้อความ rev {0}", ev.rev)}>
                  <div class="my-2 whitespace-pre-wrap rounded-lg border border-rule bg-sunk px-3.5 py-3 text-ink">{ev.body}</div>
                </Fold>
              )}
            </li>
          ))}
        </ul>
      </Fold>
      <div class="mt-2.5">
        <button disabled={busy} class="btn-quiet" onClick={() => act(() => api(`/api/sq/sends/${s.id}/archive`, { method: "POST", json: {} }))}>
          {s.archived ? t("เอาออกจากที่เก็บ") : t("เก็บเข้าที่เก็บ")}
        </button>
      </div>
    </article>
  );
}

function Fold({ label, children }: { label: string; children: any }) {
  return (
    <details class="mt-2">
      <summary class="cursor-pointer text-[13.5px] text-muted">{label}</summary>
      {children}
    </details>
  );
}

function OneLine(p: { placeholder: string; button: string; busy: boolean; send: (v: string) => Promise<void> | void; multiline?: boolean; allowEmpty?: boolean }) {
  const [v, setV] = useState("");
  return (
    <form
      class="mt-2 flex gap-2 max-sm:flex-col"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!p.allowEmpty && !v.trim()) return;
        await p.send(v);
        setV("");
      }}
    >
      {p.multiline ? (
        <textarea rows={2} value={v} onInput={(e) => setV(e.currentTarget.value)} placeholder={p.placeholder} aria-label={p.button} class="field flex-1" />
      ) : (
        <input value={v} onInput={(e) => setV(e.currentTarget.value)} placeholder={p.placeholder} aria-label={p.button} class="field flex-1" />
      )}
      <button disabled={p.busy} class="btn">{p.button}</button>
    </form>
  );
}
