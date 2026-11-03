// Per-chat settings (superusers only): forward this chat's new messages to a Discord webhook.
// The rule is a `forwards` record; its ledger rows (`forward_log`) show each post's hash and
// the HTTP result, so a duplicate or a failed post is visible here.
import { useState } from "preact/hooks";
import { api, getAuth } from "./api";
import { usePoll } from "./hooks";
import { pbDate } from "./time";
import { t } from "./i18n";

export type Rule = { id: string; name: string; thread_key: string; webhook: string; enabled: boolean; created: string; proposed_by?: string };
export type Ledger = { id: string; forward: string; mid: string; hash: string; status: string; http: number; error: string; created: string };

const f = encodeURIComponent;
export const STATUS_CLS: Record<string, string> = {
  sent: "bg-green/15 text-green", failed: "bg-red/15 text-red", sending: "bg-amber/15 text-amber",
};

export function ChatSettings(p: { platform: "Messenger" | "LINE"; chatKey: string; chatName: string; alias?: string; onAlias: () => void; close: () => void }) {
  return (
    <div class="border-b border-rule bg-card px-4 py-3 text-[13.5px]">
      <div class="mb-2 flex items-center gap-2">
        <b class="text-[15px]">{t("ตั้งค่าแชทนี้")}</b>
        <button onClick={p.close} class="btn-quiet ml-auto">{t("ปิด")}</button>
      </div>
      <AliasForm {...p} />
      {p.platform === "Messenger" ? <Forwarding threadKey={p.chatKey} chatName={p.chatName} /> : (
        <p class="mt-3 text-xs text-muted">{t("ส่งต่อไป Discord ทำได้เฉพาะแชท Messenger (LINE ยังไม่ได้เก็บลง archive)")}</p>
      )}
    </div>
  );
}

// Our own name for the chat; empty removes it.
function AliasForm({ platform, chatKey, alias, onAlias }: { platform: string; chatKey: string; alias?: string; onAlias: () => void }) {
  const [v, setV] = useState(alias || "");
  const [msg, setMsg] = useState("");
  return (
    <form class="flex items-center gap-2 max-sm:flex-col max-sm:items-stretch"
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          await api("/api/aliases", { method: "POST", json: { platform, key: chatKey, alias: v } });
          setMsg(v.trim() ? t("บันทึกชื่อเล่นแล้ว") : t("ลบชื่อเล่นแล้ว"));
          onAlias();
        } catch (x: any) {
          setMsg(x.message);
        }
      }}>
      <span class="flex-none font-semibold">{t("ชื่อเล่นของแชท")}</span>
      <input value={v} onInput={(e) => setV(e.currentTarget.value)} placeholder={t("เช่น นน, ทีมเบียร์")} class="field flex-1" aria-label={t("ชื่อเล่นของแชท")} />
      <button class="btn">{t("บันทึก")}</button>
      {msg && <span class="text-xs text-muted">{msg}</span>}
    </form>
  );
}

// Discord / webhook forwarding of this chat's new messages (superusers only).
function Forwarding({ threadKey, chatName }: { threadKey: string; chatName: string }) {
  const superuser = getAuth()?.col === "_superusers";
  const { data, reload } = usePoll(async () => {
    const rules = (await api(`/api/collections/forwards/records?perPage=10&filter=${f(`thread_key="${threadKey}"`)}`)).items as Rule[];
    const log = rules.length
      ? ((await api(`/api/collections/forward_log/records?perPage=20&sort=-created&filter=${f(rules.map((r) => `forward="${r.id}"`).join(" || "))}`)).items as Ledger[])
      : [];
    return { rule: rules[0] as Rule | undefined, log };
  }, [threadKey], 10000);
  const [hook, setHook] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const save = async (patch: Partial<Rule>) => {
    setBusy(true);
    setErr("");
    try {
      if (data?.rule) await api(`/api/collections/forwards/records/${data.rule.id}`, { method: "PATCH", json: patch });
      else await api(`/api/collections/forwards/records`, { method: "POST", json: { name: chatName, platform: "Messenger", thread_key: threadKey, enabled: true, ...patch } });
      setHook("");
      reload();
    } catch (e: any) {
      setErr(e.message);
    }
    setBusy(false);
  };

  return (
    <div class="mt-3 border-t border-dashed border-rule pt-3">
      {!superuser ? (
        <p class="text-muted">{t("การส่งต่อไป Discord ตั้งได้เฉพาะ superuser")}</p>
      ) : !data ? (
        <p class="text-muted">{t("กำลังโหลด…")}</p>
      ) : (
        <>
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-semibold">{t("ส่งต่อข้อความใหม่ไป Discord")}</span>
            {data.rule ? (
              <>
                <span class={`rounded-full px-2 py-px text-xs font-semibold ${data.rule.enabled ? "bg-green/15 text-green" : "bg-raised text-muted"}`}>
                  {data.rule.enabled ? t("เปิดอยู่") : t("ปิดอยู่")}
                </span>
                <button disabled={busy} onClick={() => save({ enabled: !data.rule!.enabled })} class="btn min-h-8 py-1">
                  {data.rule.enabled ? t("ปิด") : t("เปิด")}
                </button>
                {data.rule.proposed_by && !data.rule.enabled && <span class="text-amber">{t("เสนอโดย {0} — ยังไม่เปิด", data.rule.proposed_by)}</span>}
                <span class="truncate text-muted" title="webhook">{(data.rule.webhook || t("webhook ซ่อนอยู่")).replace(/\/[^/]+$/, "/…")}</span>
              </>
            ) : (
              <span class="text-muted">{t("ยังไม่ได้ตั้ง")}</span>
            )}
          </div>
          <form class="mt-2 flex gap-2 max-sm:flex-col" onSubmit={(e) => (e.preventDefault(), hook.trim() && save({ webhook: hook.trim() }))}>
            <input value={hook} onInput={(e) => setHook(e.currentTarget.value)} placeholder="https://discord.com/api/webhooks/…" class="field flex-1" aria-label="Discord webhook" />
            <button disabled={busy || !hook.trim()} class="btn">{data.rule ? t("เปลี่ยน webhook") : t("เปิดส่งต่อ")}</button>
          </form>
          <p class="mt-1.5 text-xs text-muted">{t("ส่งต่อเฉพาะข้อความที่เข้ามาหลังตั้งกฎ · แต่ละข้อความส่งครั้งเดียว (กันซ้ำด้วย hash) · ปิด @mention ทั้งหมด")}</p>
          {err && <p class="mt-1 text-red">{err}</p>}
          {data.log.length > 0 && (
            <ul class="mt-2.5 max-h-48 overflow-y-auto">
              {data.log.map((l) => <LedgerRow key={l.id} l={l} />)}
            </ul>
          )}
        </>
      )}
    </div>
  );
}

export function LedgerRow({ l, rule }: { l: Ledger; rule?: string }) {
  return (
    <li class="flex flex-wrap items-center gap-x-2.5 gap-y-0.5 border-t border-dashed border-rule py-1.5 text-[12.5px]">
      <span class={`rounded-full px-2 py-px text-[11px] font-semibold ${STATUS_CLS[l.status] || ""}`}>{l.status}</span>
      <span class="text-muted">HTTP {l.http || "—"}</span>
      <code class="text-muted" title={l.hash}>{l.hash.slice(0, 12)}</code>
      {rule && <span>{rule}</span>}
      <span class="ml-auto text-muted">{pbDate(l.created)}</span>
      {l.error && <span class="w-full text-red">{l.error}</span>}
    </li>
  );
}
