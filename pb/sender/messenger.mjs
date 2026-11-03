#!/usr/bin/env node
// The Messenger sender: delivers drafts a reviewer approved in the send-queue, through the
// Messenger Surrogate extension (nazt/messenger-surrogate: hub on ws://127.0.0.1:8787, the
// extension in Chrome on messenger.com). A fixed program — it decides nothing:
//
//   1. outbox    GET  /api/sq/outbox?platform=Messenger      (role=sender, never the agent)
//   2. hub       the extension must be attached, or nothing is claimed
//   3. claim     POST /api/sq/sends/{id}/claim {rev}          approved → sending, text locked
//   4. deliver   hub: draft {threadId,text} → approve <hash> → send <hash>
//                (the extension re-checks the thread in the URL and the hash of the bytes)
//   5. done      POST /api/sq/sends/{id}/done {rev, ok, proof}  sent only if the extension
//                saw the thread's last row carry this text; anything else is failed
//
// One message per run of the loop, no retries: a failed send stays failed and visible.
//
//   SQ_API=… SQ_SENDER_EMAIL=… SQ_SENDER_PASSWORD=… node pb/sender/messenger.mjs [--once] [--check]
//   --check: log in, read the outbox and the hub status, claim nothing.

const API = process.env.SQ_API || "http://phone.example.ts.net:8097";
const HUB = process.env.HUB || "ws://127.0.0.1:8787";
const EVERY = Number(process.env.EVERY_MS || 10000);
const once = process.argv.includes("--once"), check = process.argv.includes("--check");
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);

let token = "";
async function pb(path, opts = {}) {
  const r = await fetch(API + path, {
    method: opts.method || "GET",
    headers: { ...(token ? { Authorization: token } : {}), ...(opts.json ? { "content-type": "application/json" } : {}) },
    body: opts.json ? JSON.stringify(opts.json) : undefined,
  });
  const j = await r.json().catch(() => ({}));
  return { status: r.status, ok: r.ok, body: j };
}

async function login() {
  const { SQ_SENDER_EMAIL: identity, SQ_SENDER_PASSWORD: password } = process.env;
  if (!identity || !password) throw new Error("SQ_SENDER_EMAIL and SQ_SENDER_PASSWORD are required");
  const r = await pb("/api/collections/users/auth-with-password", { method: "POST", json: { identity, password } });
  if (!r.ok) throw new Error("sender login failed: " + (r.body.message || r.status));
  if (r.body.record?.role !== "sender") throw new Error(`account role is "${r.body.record?.role}", not sender — refusing`);
  token = r.body.token;
}

// One hub session: send a line, collect events until `until(ev)` says stop or time runs out.
// The hub replays recent history on connect, so anything stamped before we connected is ignored.
function hub(line, until, ms = 30000) {
  return new Promise((resolve, reject) => {
    const t0 = Date.now(), seen = [];
    const ws = new WebSocket(HUB);
    const timer = setTimeout(() => (ws.close(), resolve({ timeout: true, seen })), ms);
    ws.onopen = () => setTimeout(() => ws.send(line), 150);
    ws.onerror = () => (clearTimeout(timer), reject(new Error("hub not reachable at " + HUB)));
    ws.onmessage = (m) => {
      let ev;
      try { ev = JSON.parse(m.data); } catch { return; }
      if (typeof ev.at === "number" && ev.at < t0) return;
      if (ev.ev === "tick" || ev.ev === "telemetry") return;
      seen.push(ev);
      const hit = until(ev);
      if (hit) (clearTimeout(timer), ws.close(), resolve({ ev: hit === true ? ev : hit, seen }));
    };
  });
}

const hubStatus = () => hub("status", (e) => e.ev === "status", 5000).then((r) => r.ev || null);

async function deliver(item) {
  const { id, rev, threadKey, message } = item;
  const fail = (why) => pb(`/api/sq/sends/${id}/done`, { method: "POST", json: { rev, ok: false, proof: "sender: " + why } })
    .then((r) => log(`#${id} failed — ${why} (${r.status})`));

  const claim = await pb(`/api/sq/sends/${id}/claim`, { method: "POST", json: { rev } });
  if (!claim.ok) return log(`#${id} not claimed: ${claim.body.error || claim.status}`);
  log(`#${id} claimed rev ${rev} → ${item.chat} (${threadKey})`);
  if (claim.body.message !== message) return fail("text changed between outbox and claim");
  if (!/^\d{5,24}$/.test(threadKey || "")) return fail(`no usable threadKey ("${threadKey || ""}") — queue the draft with --thread`);

  try {
    const d = await hub(JSON.stringify({ verb: "draft", threadId: threadKey, text: message }),
      (e) => (e.ev === "draft" && e.threadId === threadKey) || (e.ev === "error" && e.reason));
    if (!d.ev || d.ev.ev !== "draft") return fail("extension did not take the draft: " + (d.ev?.reason || "timeout"));
    if (d.ev.chars !== message.length) return fail(`extension stored ${d.ev.chars} chars, approved text has ${message.length}`);
    const hash = d.ev.hash;

    const a = await hub(`approve ${hash}`, (e) => (e.ev === "approved" && e.hash === hash) || (e.ev === "error" && e.reason));
    if (!a.ev || a.ev.ev !== "approved") return fail("extension approve failed: " + (a.ev?.reason || "timeout"));

    const s = await hub(`send ${hash}`, (e) =>
      (["sent", "submissionUnverified"].includes(e.ev) && e.hash === hash) || (e.ev === "error" && (e.hash === hash || e.reason)), 90000);
    const ev = s.ev;
    if (ev?.ev === "sent") {
      const proof = `extension verified the last row of thread ${threadKey} carries the approved text (tier ${ev.tier}, hash ${hash.slice(0, 12)})`;
      const r = await pb(`/api/sq/sends/${id}/done`, { method: "POST", json: { rev, ok: true, proof } });
      return log(`#${id} SENT — ${proof} (${r.status})`);
    }
    return fail(ev?.ev === "submissionUnverified"
      ? `submitted but NOT verified (${ev.why || "no new row"}) — check the thread before doing anything`
      : "send failed: " + (ev?.reason || "timeout after 90 s"));
  } catch (e) {
    return fail(e.message);
  }
}

async function pass() {
  const out = await pb("/api/sq/outbox?platform=Messenger");
  if (out.status === 401) await login();
  if (!out.ok) return log("outbox:", out.body.message || out.status);
  const st = await hubStatus().catch((e) => ({ error: e.message }));
  const attached = !!st?.identity;
  if (check || out.body.length) log(`outbox ${out.body.length} · hub ${st?.error || (attached ? "extension attached" : "NO extension attached")}`);
  if (check) {
    for (const i of out.body) log(`  #${i.id} rev ${i.rev} ${i.chat} thread=${i.threadKey || "-"} ${JSON.stringify(i.message.slice(0, 40))}`);
    return;
  }
  if (!out.body.length || !attached) return; // nothing to do, or nowhere to send it: claim nothing
  await deliver(out.body[0]);
}

await login();
log(`sender up · ${API} · hub ${HUB}${check ? " · --check (claims nothing)" : ""}`);
do {
  await pass().catch((e) => log("pass:", e.message));
  if (once || check) break;
  await new Promise((r) => setTimeout(r, EVERY));
} while (true);
