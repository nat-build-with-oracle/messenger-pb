// Chat tabs, laid out like messenger.com: chat list on the left, conversation on the right
// (one pane at a time on a phone). Read-only: replies go through the send-queue.
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { api, fileToken, getAuth, withToken } from "./api";
import { usePoll } from "./hooks";
import { Empty, ICON_CHAT, ICON_LIST, PaneTitle, Refresh, Resting, TwoPane } from "./pane";
import { ChatSettings } from "./settings";
import { ago, stamp } from "./time";
import { t } from "./i18n";
import { privacy, pvName, pvText } from "./privacy";

// The phone owner's Messenger id (ownerID in source.go): their messages sit on the right.
const OWNER = "896050346";
// msys attachment_type (native/msys.go): 1 sticker, 2 image, 3/15 gif, 4 video, 7 link or file card.
const ATT: Record<number, string> = { 1: t("สติกเกอร์"), 2: t("รูป"), 3: "GIF", 15: "GIF", 4: t("วิดีโอ"), 7: t("ลิงก์/ไฟล์") };

export type Att = { id: string; type: number; w?: number; h?: number; title?: string; mime?: string; url?: string };
export type Msg = {
  id: string; sender: string; sender_name: string; ts: number; kind: string; text: string;
  atts?: Att[] | null; reply?: { text?: string; snippet?: string } | null;
};
type Item = { key: string; name: string; last: number; preview: string; pic?: string; tag?: string; waiting?: boolean; request?: boolean };
type Conv = { name: string; group: boolean; total: number; items: Msg[]; pic?: string };
// `also`: keys whose alias matches the search, per platform, so an alias finds its chat.
// `only`: waiting = the newest message is from someone else (ยังไม่ตอบ);
// requests = Messenger message requests (folder "pending", outside the inbox).
type Only = "all" | "waiting" | "requests";
type Also = { Messenger: string[]; LINE: string[]; only: Only };
type Source = { hash: string; title: string; tags?: boolean; list(q: string, also: Also): Promise<Item[]>; conv(key: string, limit: number): Promise<Conv> };

// Which platform + raw key a list key means in each tab ("c:…" / "l:…" in รวม).
export function ident(hash: string, key: string): { platform: "Messenger" | "LINE"; key: string } {
  if (hash === "a") return { platform: key.startsWith("l:") ? "LINE" : "Messenger", key: key.slice(2) };
  return { platform: hash === "l" ? "LINE" : "Messenger", key };
}

const f = (s: string) => encodeURIComponent(s);

// A picture keeps the token it first loaded with: a newer token would change its URL and
// make the browser fetch it again every two minutes.
function useFirst(tok: string) {
  const r = useRef("");
  if (!r.current && tok) r.current = tok;
  return r.current;
}

// Messenger: the archive collections, filled by the sync loop from the phone's live dbs.
const messenger: Source = {
  hash: "c", title: "Messenger",
  async list(q, also) {
    q = q.replace(/["\\]/g, "");
    const parts = [];
    if (q) parts.push("(" + [`name~"${q}"`, `preview~"${q}"`, ...also.Messenger.map((k) => `key="${k.replace(/"/g, "")}"`)].join(" || ") + ")");
    if (also.only === "waiting") parts.push("waiting=true");
    if (also.only === "requests") parts.push('folder="pending"');
    const filter = parts.length ? `&filter=${f(parts.join(" && "))}` : "";
    const r = await api(`/api/collections/threads/records?perPage=200&sort=-last_ts&fields=key,name,is_group,last_ts,preview,waiting,folder${filter}`);
    return r.items.map((t: any) => ({ key: t.key, name: t.name, last: t.last_ts, preview: t.preview, pic: t.key, tag: "Messenger",
      waiting: t.waiting, request: t.folder === "pending" }));
  },
  // The newest `limit` messages, in pages of 500 (PocketBase caps a page).
  async conv(key, limit) {
    const th = await api(`/api/collections/threads/records?perPage=1&filter=${f(`key="${key}"`)}`);
    const items: Msg[] = [];
    let total = 0;
    const per = Math.min(500, limit);
    for (let page = 1; items.length < limit; page++) {
      const r = await api(`/api/collections/messages/records?perPage=${per}&page=${page}&sort=-ts&filter=${f(`thread_key="${key}"`)}`);
      total = r.totalItems;
      items.push(...r.items);
      if (r.items.length < per || items.length >= total) break;
    }
    const t = th.items[0] || {};
    return { name: t.name || key, group: !!t.is_group, total, items: items.slice(0, limit).reverse(), pic: key };
  },
};

// LINE: read live from the LINE app's own db by line.go; no pictures.
const line: Source = {
  hash: "l", title: "LINE",
  async list(q, also) {
    if (also.only === "requests") return []; // LINE requests are not read yet
    const all = await api<any[]>("/api/line/chats"), n = q.toLowerCase();
    return all
      .filter((c) => !n || (c.name + " " + c.preview).toLowerCase().includes(n) || also.LINE.includes(c.id))
      .filter((c) => also.only !== "waiting" || c.waiting)
      .map((c) => ({ key: c.id, name: c.name, last: c.last, preview: c.preview, tag: "LINE", waiting: c.waiting }));
  },
  async conv(key, limit) {
    const r = await api(`/api/line/chats/${f(key)}?limit=${limit}`);
    const items: Msg[] = r.items.reverse().map((m: any) => ({
      id: "l" + m.id, sender: m.me ? OWNER : m.from, sender_name: m.name || t("ไม่ทราบชื่อ"),
      ts: m.ts, kind: m.kind === "system" ? "admin" : "text",
      // A photo the LINE app still keeps on the phone is shown; the rest stay a label.
      text: m.photo ? "" : m.text,
      atts: m.photo ? [{ id: `l${m.id}`, type: 2, mime: "image/jpeg", url: `/chat/line/${key}/${m.id}` }] : undefined,
    }));
    return { name: r.name || key, group: !key.startsWith("u"), total: r.total, items };
  },
};

// Both in one list, newest first; keys carry their source ("c:<thread>", "l:<chat>").
const inbox: Source = {
  hash: "a", title: t("รวมทุกแชท"), tags: true,
  async list(q, also) {
    const [m, l] = await Promise.all([messenger.list(q, also), line.list(q, also).catch(() => [] as Item[])]);
    return [...m.map((t) => ({ ...t, key: "c:" + t.key })), ...l.map((t) => ({ ...t, key: "l:" + t.key }))].sort((a, b) => b.last - a.last);
  },
  conv: (key, limit) => (key.startsWith("l:") ? line : messenger).conv(key.slice(2), limit),
};

const SOURCES: Record<string, Source> = { inbox, chats: messenger, line };
export const CHAT_TABS: [string, string][] = [["inbox", t("รวม")], ["chats", "Messenger"], ["line", "LINE"]];
const BY_HASH: Record<string, string> = { a: "inbox", c: "chats", l: "line" };
export function routeOf(hash: string) {
  const m = hash.match(/^#([acl])=([\w:.-]{1,70})$/);
  return m ? { tab: BY_HASH[m[1]], key: m[2] } : null;
}

export function ChatView({ tab }: { tab: string }) {
  const src = SOURCES[tab];
  const [q, setQ] = useState("");
  const [hash, setHash] = useState(location.hash);
  useEffect(() => {
    const on = () => setHash(location.hash);
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  }, []);
  const r = routeOf(hash);
  const key = r?.tab === tab ? r.key : "";
  const tok = usePoll(fileToken, [], 60e3).data || "";
  const al = useAliases();
  const [only, setOnly] = useState<Only>("all");
  const list = usePoll(() => {
    const n = q.trim().toLowerCase(), also: Also = { Messenger: [], LINE: [], only };
    if (n) for (const [k, a] of al.map) if (a.toLowerCase().includes(n)) also[k.split(":")[0] as "Messenger" | "LINE"].push(k.slice(k.indexOf(":") + 1));
    return src.list(q.trim(), also);
  }, [tab, q, al.map.size, only]);

  return (
    <TwoPane
      open={!!key}
      list={
        <>
          <PaneTitle title={src.title} home={() => ((location.hash = ""), setQ(""))} refresh={list.reload} loading={list.loading} />
          <label class="relative mx-4 mb-2.5 block">
            <svg viewBox="0 0 24 24" class="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4" /></svg>
            <input type="search" value={q} onInput={(e) => setQ(e.currentTarget.value)} placeholder={t("ค้นหาแชท")} aria-label={t("ค้นหาแชท")}
              class="w-full rounded-full bg-raised py-2 pl-10 pr-4 outline-none placeholder:text-muted focus:bg-raised-hi" />
          </label>
          <div class="mx-4 mb-2 flex gap-1.5" role="group" aria-label={t("ตัวกรอง")}>
            {([["all", t("ทั้งหมด")], ["waiting", t("ยังไม่ตอบ")], ...(src.hash === "l" ? [] : [["requests", t("คำขอข้อความ")]])] as [Only, string][]).map(([v, label]) => (
              <button key={v} onClick={() => setOnly(v)} aria-pressed={only === v}
                class={`rounded-full px-3 py-1 text-[13px] ${only === v ? "bg-select text-select-ink" : "bg-raised text-muted hover:text-ink"}`}>
                {label}
              </button>
            ))}
          </div>
          <ul class="flex-1 overflow-y-auto px-2 pb-2">
            {list.data?.map((it) => (
              <li key={it.key} class="group relative">
                <a href={`#${src.hash}=${it.key}`} class={`flex items-center gap-3 rounded-xl px-2.5 py-2 transition-colors duration-150 ${it.key === key ? "bg-select" : "hover:bg-hover"}`}>
                  <Avatar id={it.pic} seed={it.key} name={pvName(it.name, it.key)} size={48} tok={tok} />
                  <div class="min-w-0 flex-1">
                    <div class={`truncate text-[15px] ${it.waiting ? "font-semibold" : ""}`}>
                      {src.tags && <Tag tag={it.tag} />}
                    {it.request && <span class="mr-1.5 rounded bg-amber/15 px-1.5 py-px align-[2px] text-[10.5px] font-bold text-amber">{t("คำขอ")}</span>}
                      <Named alias={al.get(ident(src.hash, it.key))} name={it.name || it.key} />
                    </div>
                    <div class="flex gap-1 text-[13px] text-muted">
                      <span class="truncate">{pvText(it.preview)}</span>
                      {it.last ? <span class="flex-none">· {ago(it.last)}</span> : null}
                      {it.waiting && <span title={t("ยังไม่ตอบ")} class="ml-auto mt-1 size-2.5 flex-none rounded-full bg-mine" />}
                    </div>
                  </div>
                </a>
                <OpenIn id={ident(src.hash, it.key)} name={it.name || it.key} shown={it.key === key} />
              </li>
            ))}
            {list.data && !list.data.length && <li class="py-12 text-center text-muted">{t("ไม่มีแชท")}</li>}
            {list.error && <li class="py-12 text-center text-red">{list.error}</li>}
          </ul>
        </>
      }
      detail={key ? <Conversation key={tab + key} src={src} chatKey={key} tok={tok} /> : <Resting icon={ICON_CHAT} title={t("เลือกแชททางซ้าย")} hint={t("แชทที่มีจุดสีน้ำเงินคือยังไม่ได้ตอบ")} />}
    />
  );
}

// Who has read how far (msys participants), and the read history the sync logs (seen_log).
// Scroll to the newest loaded message at or before `ts` and flash it. When it is older than
// what is loaded, `older` loads back to it first (and jumps again once it is on screen).
type Older = (ts: number, who: string) => void;
function jumpTo(from: Element, ts: number, who: string, older?: Older) {
  const conv = from.closest("[data-conv]");
  if (!conv) return;
  let hit: HTMLElement | null = null;
  const els = [...conv.querySelectorAll<HTMLElement>("[data-ts]")];
  for (const el of els) if (Number(el.dataset.ts) <= ts) hit = el;
  if (!hit && older) return older(ts, who);
  if (!hit) hit = els[0] || null; // older than everything the archive has: show where the chat starts
  if (!hit) return;
  hit.scrollIntoView({ block: "center", behavior: "smooth" });
  hit.classList.add("ring-2", "ring-amber", "ring-offset-2", "ring-offset-panel");
  setTimeout(() => hit!.classList.remove("ring-2", "ring-amber", "ring-offset-2", "ring-offset-panel"), 2500);
}

function Seen({ threadKey, lastTs, tok, older }: { threadKey: string; lastTs: number; tok: string; older: Older }) {
  const [open, setOpen] = useState(false);
  const { data } = usePoll(async () => {
    const seen = (await api(`/api/collections/seen/records?perPage=200&sort=-read_ts&filter=${f(`thread_key="${threadKey}"`)}`)).items as any[];
    const log = open ? ((await api(`/api/collections/seen_log/records?perPage=100&sort=-read_ts&filter=${f(`thread_key="${threadKey}"`)}`)).items as any[]) : [];
    return { seen, log };
  }, [threadKey, open], 10000);
  if (!data || !data.seen.length) return null;
  const all = data.seen.filter((p) => p.read_ts >= lastTs), behind = data.seen.filter((p) => p.read_ts < lastTs);
  return (
    <div class="mt-2 flex max-w-full flex-col items-end self-stretch text-right text-[12px] text-muted">
      {all.length > 0 && (
        <div class="flex items-center justify-end gap-1" title={all.map((p) => `${pvName(p.name, p.contact)} · ${stamp(p.read_ts)}`).join("\n")}>
          <span>{t("อ่านแล้ว")}</span>
          {all.slice(0, 8).map((p) => <Avatar key={p.contact} id={p.contact} seed={p.contact} name={pvName(p.name, p.contact)} size={16} tok={tok} />)}
          {all.length > 8 && <span>+{all.length - 8}</span>}
        </div>
      )}
      {behind.length > 0 && (
        <div class="mt-1.5 flex max-w-full flex-wrap items-center justify-end gap-1.5">
          <span>{t("ยังไม่อ่านล่าสุด:")}</span>
          {behind.map((p) => (
            <button key={p.contact} onClick={(e) => jumpTo(e.currentTarget, p.read_ts, p.name, older)} title={`${pvName(p.name, p.contact)} · ${t("เลื่อนไปข้อความที่อ่านถึง")}`}
              class="flex items-center gap-1.5 rounded-full bg-raised py-0.5 pl-0.5 pr-2.5 text-muted hover:bg-raised-hi hover:text-ink">
              <Avatar id={p.contact} seed={p.contact} name={pvName(p.name, p.contact)} size={18} tok={tok} />
              <span class="max-w-[120px] truncate text-ink">{pvName(p.name, p.contact).split(" ")[0]}</span>
              <span class="tabular-nums">{stamp(p.read_ts)}</span>
            </button>
          ))}
        </div>
      )}
      <button onClick={() => setOpen(!open)} class="mt-0.5 text-[12px] text-muted underline-offset-2 hover:text-ink hover:underline">
        {open ? t("ซ่อนประวัติการอ่าน") : t("ประวัติการอ่าน")}
      </button>
      {open && (
        <ul class="mt-1 max-h-52 overflow-y-auto text-left">
          {data.log.map((l) => (
            <li key={l.id} class="border-t border-dashed border-rule py-1">
              <button onClick={(e) => jumpTo(e.currentTarget, l.read_ts, l.name, older)} class="text-left hover:text-ink" title={t("เลื่อนไปข้อความที่อ่านถึง")}>
                <b class="font-semibold text-ink">{pvName(l.name, l.contact)}</b> {t("อ่านถึง {0}", stamp(l.read_ts))} <span class="text-muted">{t("(ก่อนหน้า {0})", stamp(l.prev_ts))}</span>
              </button>
            </li>
          ))}
          {!data.log.length && <li class="py-1">{t("ยังไม่มีประวัติ — เริ่มเก็บตั้งแต่วันนี้ ทุกครั้งที่มีคนอ่านเพิ่ม")}</li>}
        </ul>
      )}
    </div>
  );
}

// Our alias in front, the real name after it in grey.
function Named({ alias, name }: { alias?: string; name: string }) {
  if (privacy) return <>{pvName(name)}</>;
  return alias ? <>{alias} <span class="font-normal text-muted">· {name}</span></> : <>{name}</>;
}

// Aliases for every chat (collection `aliases`), refreshed every 15 s.
function useAliases() {
  const { data, reload } = usePoll(async () => {
    const r = await api("/api/collections/aliases/records?perPage=500&fields=platform,key,alias");
    return new Map<string, string>(r.items.map((a: any) => [a.platform + ":" + a.key, a.alias]));
  }, [], 15000);
  const map = data || new Map<string, string>();
  return { map, reload, get: (i: { platform: string; key: string }) => map.get(i.platform + ":" + i.key) };
}

function Tag({ tag }: { tag?: string }) {
  if (!tag) return null;
  const line = tag === "LINE";
  return (
    <span class={`mr-1.5 rounded-md px-1.5 py-px align-[2px] text-[10.5px] font-semibold ${line ? "bg-[#06c755]/12 text-line-ink" : "bg-mine/12 text-select-ink/90"}`}>{tag}</span>
  );
}

function Conversation({ src, chatKey, tok }: { src: Source; chatKey: string; tok: string }) {
  // How many of the newest messages to show; scrolling to the top asks for 200 more.
  const limit = useRef(200), anchor = useRef<number | null>(null), fetching = useRef(false);
  const jump = useRef<{ ts: number; who: string } | null>(null);
  const toTop = useRef(false);
  const { data, error, loading, reload } = usePoll(() => src.conv(chatKey, limit.current), [chatKey]);
  const more = data ? data.total > data.items.length : false;
  const [gear, setGear] = useState(false);
  // Settings exist for Messenger chats (the ones the archive holds): #c=<key> or #a=c:<key>.
  const id = ident(src.hash, chatKey);
  const al = useAliases();
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(true); // follow new messages while the reader is at the bottom
  const sig = data ? data.items.map((m) => m.id).join() : "";
  useLayoutEffect(() => {
    const b = box.current;
    if (!b) return;
    if (toTop.current) (b.scrollTop = 0), (toTop.current = false), (stick.current = false);
    else if (anchor.current !== null) b.scrollTop = b.scrollHeight - anchor.current; // older ones went in above: stay put
    else if (stick.current) b.scrollTop = b.scrollHeight;
    anchor.current = null;
    fetching.current = false;
    if (jump.current) {
      const j = jump.current;
      jump.current = null;
      setTimeout(() => jumpTo(b, j.ts, j.who), 50);
    }
  }, [sig]);
  const loadOlder = (n: number) => {
    const b = box.current!;
    fetching.current = true;
    anchor.current = b.scrollHeight - b.scrollTop;
    limit.current = n;
    reload();
  };
  const onScroll = () => {
    const b = box.current!;
    stick.current = b.scrollTop + b.clientHeight >= b.scrollHeight - 80;
    if (b.scrollTop < 150 && more && !fetching.current) loadOlder(limit.current + 200);
  };
  // A reader's read point older than what is loaded: load back to it, then jump.
  const older: Older = async (ts, who) => {
    if (id.platform !== "Messenger" || !more) return jumpTo(box.current!, ts, who); // nothing older to load
    const r = await api(`/api/collections/messages/records?perPage=1&fields=id&filter=${f(`thread_key="${id.key}" && ts>=${ts}`)}`);
    jump.current = { ts, who };
    stick.current = false;
    loadOlder(Math.max(limit.current, r.totalItems + 20));
  };
  const onImg = () => stick.current && box.current && (box.current.scrollTop = box.current.scrollHeight);

  return (
    <>
      <div class="flex items-center gap-2.5 border-b border-rule px-4 py-2.5">
        <a href="#" class="grid size-8 flex-none place-items-center rounded-full text-muted hover:bg-raised md:hidden" aria-label={t("กลับ")}><svg viewBox="0 0 24 24" class="size-5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6" /></svg></a>
        <Avatar id={data?.pic} seed={chatKey} name={pvName(data?.name || "", chatKey)} size={40} tok={tok} />
        <b class="min-w-0 flex-1 truncate text-base sm:flex-none"><Named alias={al.get(id)} name={data?.name || ""} /></b>
        <OpenIn id={ident(src.hash, chatKey)} pill />
        <DraftCommand link={`#${src.hash}=${chatKey}`} />
        <span class="ml-auto flex-none text-[12.5px] text-muted max-sm:hidden">
          {data && (data.total > data.items.length ? t("{0} ล่าสุด จาก {1}", data.items.length, data.total) : t("{0} ข้อความ", data.items.length))}
        </span>
        <button title={t("ไปข้อความแรกสุด")} aria-label={t("ไปข้อความแรกสุด")} onClick={() => {
          const b = box.current;
          if (!data || !b) return;
          if (!more) return void (b.scrollTop = 0);
          toTop.current = true; stick.current = false; fetching.current = true; limit.current = data.total; reload();
        }} class="grid size-9 flex-none place-items-center rounded-full bg-raised text-muted hover:bg-raised-hi hover:text-ink max-sm:hidden"><svg viewBox="0 0 24 24" class="size-[18px]" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 4h14M12 20V9m-5 5 5-5 5 5" /></svg></button>
        <button title={t("ไปข้อความล่าสุด")} aria-label={t("ไปข้อความล่าสุด")} onClick={() => {
          const b = box.current;
          if (b) (b.scrollTop = b.scrollHeight), (stick.current = true);
        }} class="grid size-9 flex-none place-items-center rounded-full bg-raised text-muted hover:bg-raised-hi hover:text-ink max-sm:hidden"><svg viewBox="0 0 24 24" class="size-[18px]" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 20h14M12 4v11m-5-5 5 5 5-5" /></svg></button>
        <Refresh onClick={reload} loading={loading} />
        {(
          <button onClick={() => setGear(!gear)} title={t("ตั้งค่าแชทนี้")} aria-label={t("ตั้งค่าแชทนี้")} aria-expanded={gear}
            class={`grid size-9 flex-none place-items-center rounded-full ${gear ? "bg-mine text-white" : "bg-raised text-muted hover:bg-raised-hi hover:text-ink"}`}>
            <svg viewBox="0 0 24 24" class="size-[18px]" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
            </svg>
          </button>
        )}
      </div>
      {gear && <ChatSettings platform={id.platform} chatKey={id.key} chatName={data?.name || id.key} alias={al.get(id)} onAlias={al.reload} close={() => setGear(false)} />}
      <div ref={box} onScroll={onScroll} data-conv class="flex flex-1 flex-col overflow-y-auto px-4 py-3">
        {error && <p class="m-auto text-red">{error}</p>}
        {data && more && <div class="self-center py-2 text-xs text-muted">{fetching.current ? t("กำลังโหลดข้อความเก่า…") : t("เลื่อนขึ้นเพื่อโหลดข้อความเก่า")}</div>}
        {data && !more && data.items.length > 0 && <div class="self-center py-2 text-xs text-muted">{t("ต้นแชท")}</div>}
        {data && (data.items.length ? <Runs items={data.items} group={data.group} tok={tok} pictures={!!data.pic} onImg={onImg} /> : <p class="m-auto text-muted">{t("ไม่มีข้อความ")}</p>)}
        {data && id.platform === "Messenger" && <Seen threadKey={id.key} lastTs={data.items[data.items.length - 1]?.ts || 0} tok={tok} older={older} />}
      </div>
      <div class="border-t border-rule p-2.5 text-center text-[12.5px] text-muted">{t("อ่านอย่างเดียว — ข้อความตอบกลับเข้าคิวให้ตรวจก่อนส่ง")}</div>
    </>
  );
}

// One run = consecutive messages from one sender with less than 5 minutes between them;
// a time marker goes in after 15 quiet minutes.
function Runs({ items, group, tok, pictures, onImg }: { items: Msg[]; group: boolean; tok: string; pictures: boolean; onImg: () => void }) {
  const out: any[] = [];
  let run: Msg[] = [], prev = 0;
  const flush = () => {
    if (!run.length) return;
    const first = run[0], me = first.sender === OWNER;
    out.push(
      <div key={first.id} class={`mt-2 flex items-end gap-2 ${me ? "flex-row-reverse" : ""}`}>
        {!me && <span class="w-7 flex-none"><Avatar id={pictures ? first.sender : undefined} seed={first.sender} name={pvName(first.sender_name, first.sender)} size={28} tok={tok} /></span>}
        <div class={`flex min-w-0 max-w-[min(70%,560px)] flex-col gap-0.5 ${me ? "run-me items-end" : "run-them"}`}>
          {group && !me && <div class="mb-px ml-3 text-xs text-muted">{pvName(first.sender_name, first.sender)}</div>}
          {run.map((m) => <Bubbles key={m.id} m={m} me={me} tok={tok} onImg={onImg} />)}
        </div>
      </div>,
    );
    run = [];
  };
  for (const m of items) {
    if (m.ts - prev > 15 * 6e4) {
      flush();
      out.push(<div key={"t" + m.id} class="mb-2 mt-4 self-center text-xs text-muted">{stamp(m.ts)}</div>);
    }
    if (m.kind === "admin") {
      flush();
      out.push(<div key={m.id} data-ts={m.ts} class="mx-10 my-1.5 self-center text-center text-[12.5px] text-muted">{pvText(m.text)}</div>);
    } else {
      if (run.length && (run[0].sender !== m.sender || m.ts - prev > 5 * 6e4)) flush();
      run.push(m);
    }
    prev = m.ts;
  }
  flush();
  return <>{out}</>;
}

const bubble = "bb w-fit max-w-full whitespace-pre-wrap break-words rounded-[18px] px-3 py-[7px] text-[15px] leading-[1.35]";

// Returned as siblings (not wrapped) so the corner rules in style.css see every bubble of a run.
function Bubbles({ m, me, tok, onImg }: { m: Msg; me: boolean; tok: string; onImg: () => void }) {
  const color = me ? "bg-mine text-white" : "bg-bubble text-bubble-ink";
  if (m.kind === "unsent")
    return <div data-ts={m.ts} title={stamp(m.ts)} class={`${bubble} border border-rule italic text-muted`}>{pvText(m.text) || t("ยกเลิกการส่งข้อความ")}</div>;
  const atts = m.atts || [];
  const placeholder = atts.length > 0 && /sent an attachment\.$/.test(m.text || ""); // the app's stand-in text for a file
  return (
    <>
      {m.reply?.snippet && <div class="mx-3 mt-1.5 text-xs text-muted">↩ {pvText(m.reply.snippet)}</div>}
      {m.reply?.text && <div class={`${bubble} -mb-2.5 bg-raised pb-3.5 text-[13.5px] text-muted opacity-85`}>{pvText(m.reply.text)}</div>}
      {m.text && !placeholder && <div data-ts={m.ts} title={stamp(m.ts)} class={`${bubble} ${color}`}>{pvText(m.text)}</div>}
      {atts.map((a) => <Attachment key={a.id} a={a} me={me} tok={tok} onImg={onImg} ts={m.ts} />)}
    </>
  );
}

// Pictures come from the phone's picture cache through /chat/media (sqmedia.go).
function Attachment({ a, me, tok: live, onImg, ts }: { a: Att; me: boolean; tok: string; onImg: () => void; ts: number }) {
  const tok = useFirst(live);
  const [failed, setFailed] = useState(false);
  const label = ATT[a.type] || t("ไฟล์แนบ"), mime = a.mime || "";
  const src = withToken(a.url || `/chat/media/${f(a.id)}`, tok);
  const ratio = a.w && a.h ? { aspectRatio: `${a.w} / ${a.h}` } : undefined;
  const note = (t: string) => <span data-ts={ts} class="bb block w-fit rounded-[18px] bg-bubble px-3 py-[7px] text-[13px] text-muted">{t}</span>;
  if (!tok) return note(`[${label}]`);
  if (a.type === 6 || (mime && !/^(image|video)\//.test(mime)))
    return (
      <a data-ts={ts} href={src} target="_blank" rel="noopener" class={`${bubble} ${me ? "bg-mine text-white" : "bg-bubble"} flex items-center gap-2`}>
        📄 {pvText(a.title) || t("ไฟล์")}
      </a>
    );
  if (failed) return note(t("[{0}{1}] ไม่มีในแคชของเครื่อง", label, a.title ? " · " + a.title : ""));
  if (mime.startsWith("video/") && a.id.startsWith("e"))
    return <video data-ts={ts} src={src} controls preload="metadata" style={ratio} class="bb max-h-[360px] max-w-[min(320px,100%)] rounded-[18px]" />;
  const sticker = a.type === 1;
  return (
    <div data-ts={ts} class="bb w-fit">
      <img src={src} alt={label} style={ratio} onLoad={onImg} onError={() => setFailed(true)}
        class={sticker ? "max-w-[120px]" : "block max-h-[360px] max-w-[min(320px,100%)] rounded-[18px]"} />
      {a.type === 7 && a.title && note(pvText(a.title))}
    </div>
  );
}

const COLORS = ["#e3a53c", "#4cb07c", "#5e7ce2", "#e2685a", "#b65ee2", "#2fa3a3", "#d1663f"];
const hue = (s: string) => COLORS[[...s].reduce((a, c) => a + c.charCodeAt(0), 0) % COLORS.length];
const initials = (n: string) => (n.trim().split(/\s+/).slice(0, 2).map((w) => [...w][0]).join("") || "?").toUpperCase();

// Initials underneath; the cached picture covers them once it loads (Messenger ids only).
function Avatar({ id, seed, name, size, tok: live }: { id?: string; seed: string; name: string; size: number; tok: string }) {
  const tok = useFirst(live);
  const [gone, setGone] = useState(false);
  return (
    <span class="relative grid flex-none place-items-center overflow-hidden rounded-full font-semibold text-white"
      style={{ width: size, height: size, fontSize: Math.round(size * 0.36), background: hue(seed) }}>
      {initials(name)}
      {!privacy && id && /^\d+$/.test(id) && tok && !gone && (
        <img src={withToken(`/chat/avatar/${id}`, tok)} alt="" onError={() => setGone(true)} class="absolute inset-0 h-full w-full object-cover" />
      )}
    </span>
  );
}

// Open the chat in the app it came from. Messenger has a link per chat (messenger.com/t/<thread key>,
// new tab). LINE has no public link to one chat, so LINE opens the app on its chat list
// (line://nv/chat, the same link the phone's rotation uses); the tooltip says so.
const EXT = "M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5";
function OpenIn({ id, name, pill, shown }: { id: { platform: "Messenger" | "LINE"; key: string }; name?: string; pill?: boolean; shown?: boolean }) {
  const line = id.platform === "LINE";
  const href = line ? "line://nv/chat" : `https://www.messenger.com/t/${id.key}`;
  const title = line ? t("เปิดแอป LINE (หน้ารายการแชท — LINE ไม่มีลิงก์ไปห้องแชทตรง)") : t("เปิดแชทนี้ใน Messenger (แท็บใหม่)");
  const tone = line ? "text-line-ink bg-[#06c755]/12 hover:bg-[#06c755]/22" : "text-select-ink bg-mine/12 hover:bg-mine/20";
  const icon = <svg viewBox="0 0 24 24" class="size-3.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d={EXT} /></svg>;
  if (pill)
    return (
      <a href={href} target={line ? undefined : "_blank"} rel="noopener" title={title} class={`flex flex-none items-center gap-1 rounded-full px-2.5 py-1 text-[12px] ${tone}`}>
        {icon}
        <span class="max-sm:hidden">{id.platform}</span>
      </a>
    );
  return (
    <a href={href} target={line ? undefined : "_blank"} rel="noopener" title={title} aria-label={t("เปิด {0} ใน {1}", name || "", id.platform)}
      class={`absolute right-2.5 top-2 grid size-7 place-items-center rounded-full ${tone} opacity-0 transition-opacity duration-150 focus-visible:opacity-100 group-hover:opacity-100 ${shown ? "opacity-100" : ""} [@media(hover:none)]:hidden`}>
      {icon}
    </a>
  );
}

// "AI ร่าง": asks the reply desk on m5 for a draft of this chat (POST /api/sq/draft-requests, reviewer
// only). The desk (`just desk`) claims it, drafts with Claude and queues it as pending; this button
// follows the request and links to the draft when it lands. The server runs no AI. Agents and
// viewers get the command to copy instead (`just draft '<link>'`).
function DraftCommand({ link }: { link: string }) {
  const reviewer = getAuth()?.role === "reviewer";
  const [req, setReq] = useState<{ id: string; status: string; send?: string; note?: string } | null>(null);
  const [err, setErr] = useState("");
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!req || !["open", "taken"].includes(req.status)) return;
    const timer = setInterval(async () => {
      try {
        setReq(await api(`/api/collections/draft_requests/records/${req.id}`));
      } catch {}
    }, 4000);
    return () => clearInterval(timer);
  }, [req?.id, req?.status]);
  const cmd = `just draft '${link}'`;
  if (!reviewer)
    return (
      <button type="button" title={t("คัดลอกคำสั่งให้ AI ร่างคำตอบแชทนี้: {0}", cmd)}
        onClick={async () => {
          try { await navigator.clipboard.writeText(cmd); } catch { prompt(t("คัดลอกคำสั่งนี้"), cmd); }
          setCopied(true); setTimeout(() => setCopied(false), 1600);
        }}
        class="flex flex-none items-center gap-1 rounded-full bg-amber/12 px-2.5 py-1 text-[12px] text-amber hover:bg-amber/20">
        <Sparkle /> <span class="max-sm:hidden">{copied ? t("คัดลอกแล้ว") : t("AI ร่าง")}</span>
      </button>
    );
  const state = req?.status;
  const label =
    state === "open" ? t("รอ AI…") : state === "taken" ? t("AI กำลังร่าง…") : state === "done" ? t("ร่างแล้ว — ดูในรอตรวจ")
    : state === "skipped" ? t("ไม่มีอะไรต้องตอบ") : state === "failed" ? t("ร่างไม่สำเร็จ") : t("AI ร่าง");
  const tone = state === "done" ? "bg-green/15 text-green" : state === "failed" ? "bg-red/15 text-red" : "bg-amber/12 text-amber hover:bg-amber/20";
  if (state === "done" && req?.send)
    return (
      <a href={`#${req.send}`} class={`flex flex-none items-center gap-1 rounded-full px-2.5 py-1 text-[12px] ${tone}`} title={t("เปิดร่างคำตอบที่ AI เขียน")}>
        <Sparkle /> <span class="max-sm:hidden">{label}</span>
      </a>
    );
  return (
    <button type="button" disabled={state === "open" || state === "taken"}
      title={err || req?.note || t("ให้ AI ร่างคำตอบแชทนี้ (เข้าคิวรอตรวจ ไม่ส่งเอง)")}
      onClick={async () => {
        setErr("");
        try {
          setReq(await api("/api/sq/draft-requests", { method: "POST", json: { link } }));
        } catch (x: any) {
          setErr(x.message);
        }
      }}
      class={`flex flex-none items-center gap-1 rounded-full px-2.5 py-1 text-[12px] disabled:opacity-80 ${err ? "bg-red/15 text-red" : tone}`}>
      <Sparkle spin={state === "open" || state === "taken"} /> <span class="max-sm:hidden">{err ? t("ส่งคำขอไม่สำเร็จ") : label}</span>
    </button>
  );
}

function Sparkle({ spin }: { spin?: boolean }) {
  return (
    <svg viewBox="0 0 24 24" class={`size-3.5 ${spin ? "animate-pulse" : ""}`} fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <path d="M12 3l1.8 4.7L18.5 9.5l-4.7 1.8L12 16l-1.8-4.7L5.5 9.5l4.7-1.8z M19 15l.8 2.2 2.2.8-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z" />
    </svg>
  );
}
