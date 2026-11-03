import { t } from "./i18n";
// Talks only to this server's PocketBase API. The saved login lives in localStorage
// ("sq.auth", same key as the old page so nobody is signed out by the switch).

export type Auth = { token: string; col: string; email: string; role: string };

let auth: Auth | null = JSON.parse(localStorage.getItem("sq.auth") || "null");
const listeners = new Set<() => void>();

export const getAuth = () => auth;
export const onAuth = (fn: () => void) => (listeners.add(fn), () => listeners.delete(fn));
function setAuth(a: Auth | null) {
  auth = a;
  a ? localStorage.setItem("sq.auth", JSON.stringify(a)) : localStorage.removeItem("sq.auth");
  listeners.forEach((fn) => fn());
}
export const logout = () => setAuth(null);

export async function api<T = any>(path: string, opts: { method?: string; json?: unknown } = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (auth?.token) headers.Authorization = auth.token;
  if (opts.json !== undefined) headers["content-type"] = "application/json";
  const r = await fetch(path, {
    method: opts.method || "GET",
    headers,
    body: opts.json !== undefined ? JSON.stringify(opts.json) : undefined,
  });
  const text = await r.text();
  let j: any = {};
  try {
    j = text ? JSON.parse(text) : {};
  } catch {
    j = { message: text };
  }
  if (r.status === 401 && auth) {
    logout();
    throw new Error(t("หมดเวลาเข้าระบบ — เข้าใหม่อีกครั้ง"));
  }
  if (!r.ok) throw new Error(j.error || j.message || "HTTP " + r.status);
  return j as T;
}

export async function login(email: string, password: string) {
  for (const col of ["_superusers", "users"]) {
    const r = await fetch(`/api/collections/${col}/auth-with-password`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ identity: email, password }),
    });
    if (!r.ok) continue;
    const j = await r.json();
    setAuth({ token: j.token, col, email: j.record.email, role: col === "_superusers" ? "reviewer" : j.record.role || "" });
    return;
  }
  throw new Error(t("อีเมลหรือรหัสผ่านไม่ถูกต้อง"));
}

// #login=<token> from `messenger-pb login-link`: drop it from the address bar first (so it
// stays out of history), then ask the server who it is. The token is static: auth-refresh
// returns it unchanged, so the login ends when the link expires.
export async function loginFromLink() {
  const m = location.hash.match(/^#login=([\w.-]+)$/);
  if (!m) return;
  history.replaceState(null, "", location.pathname + location.search);
  dispatchEvent(new HashChangeEvent("hashchange"));
  const r = await fetch("/api/collections/users/auth-refresh", { method: "POST", headers: { Authorization: m[1] } });
  if (!r.ok) throw new Error(t("ลิงก์เข้าระบบหมดอายุหรือใช้ไม่ได้"));
  const j = await r.json();
  setAuth({ token: j.token, col: "users", email: j.record.email, role: j.record.role || "" });
}

// Each auth collection refreshes its own token; a failure leaves the old one to expire.
export async function refreshAuth() {
  if (!auth) return;
  try {
    const j = await api<{ token: string }>(`/api/collections/${auth.col}/auth-refresh`, { method: "POST" });
    setAuth({ ...auth, token: j.token });
  } catch {}
}

// <img> cannot send a header, so pictures carry a short-lived file token (valid ~3 min).
let fileTok = "", fileTokAt = 0;
export async function fileToken() {
  if (!fileTok || Date.now() - fileTokAt > 120e3) {
    fileTok = (await api<{ token: string }>("/api/files/token", { method: "POST" })).token;
    fileTokAt = Date.now();
  }
  return fileTok;
}
export const withToken = (path: string, tok: string) => `${path}?token=${encodeURIComponent(tok)}`;

// Passkeys (passkey.go): Touch ID / Face ID or a hardware key. Needs HTTPS (a secure context), so
// the button only shows on the tailscale-serve address, never on http://100.x.
export const passkeySupported = () => window.isSecureContext && typeof window.PublicKeyCredential === "function";

const b64u = {
  dec: (s: string) => Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, "")), (c) => c.charCodeAt(0)),
  enc: (b: ArrayBuffer | null) =>
    b ? btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : null,
};
const ids = (list?: { id: string; type: string; transports?: string[] }[]) =>
  list?.map((c) => ({ ...c, id: b64u.dec(c.id) })) as PublicKeyCredentialDescriptor[] | undefined;

async function post(path: string, body?: unknown) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.message || "HTTP " + r.status);
  return j;
}

// NotAllowedError: the person cancelled, the prompt timed out, or the key could not verify them.
const cancelled = (x: any) => x?.name === "NotAllowedError" || x?.name === "AbortError";

export async function loginWithPasskey() {
  let begun;
  try {
    begun = await post("/api/passkey/login/begin");
  } catch (x: any) {
    // 503 from a server without PASSKEY_RP_ID / PASSKEY_ORIGIN: say where passkeys do work.
    if (/not configured/i.test(x.message)) throw new Error(t("ที่อยู่นี้ยังไม่เปิด passkey — เข้าทาง https ที่ตั้งไว้ (tailscale serve) หรือใช้รหัสผ่าน"));
    throw x;
  }
  const { sid, options } = begun;
  const pk = options.publicKey;
  let cred: PublicKeyCredential;
  try {
    cred = (await navigator.credentials.get({
      publicKey: { ...pk, challenge: b64u.dec(pk.challenge), allowCredentials: ids(pk.allowCredentials) },
    })) as PublicKeyCredential;
  } catch (x) {
    if (cancelled(x)) throw new Error(t("ยกเลิก หรือยืนยันตัวตน (นิ้ว/หน้า/PIN) ไม่ผ่าน"));
    if ((x as any)?.name === "SecurityError") throw new Error(t("ที่อยู่นี้ใช้ passkey ไม่ได้ — เปิดผ่าน https ที่ตั้งไว้ หรือ localhost"));
    throw x;
  }
  const r = cred.response as AuthenticatorAssertionResponse;
  let j;
  try {
    j = await post(`/api/passkey/login/finish?sid=${encodeURIComponent(sid)}`, {
      id: cred.id, rawId: b64u.enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: b64u.enc(r.clientDataJSON), authenticatorData: b64u.enc(r.authenticatorData),
        signature: b64u.enc(r.signature), userHandle: b64u.enc(r.userHandle),
      },
    });
  } catch {
    throw new Error(t("passkey นี้เข้าระบบไม่ได้ — ใช้รหัสผ่านแทน"));
  }
  const col = j.record.collectionName;
  setAuth({ token: j.token, col, email: j.record.email, role: col === "_superusers" ? "reviewer" : j.record.role || "" });
}

export type Passkey = { id: string; name: string; created: string; last_used: string };
export const listPasskeys = () => api<{ configured: boolean; items: Passkey[] }>("/api/passkey");
export const deletePasskey = (id: string) => api(`/api/passkey/${id}`, { method: "DELETE" });

// Returns false when the person cancelled the prompt.
export async function addPasskey(name: string) {
  const { sid, options } = await api<{ sid: string; options: any }>("/api/passkey/register/begin", { method: "POST" });
  const pk = options.publicKey;
  let cred: PublicKeyCredential;
  try {
    cred = (await navigator.credentials.create({
      publicKey: {
        ...pk, challenge: b64u.dec(pk.challenge), user: { ...pk.user, id: b64u.dec(pk.user.id) },
        excludeCredentials: ids(pk.excludeCredentials),
      },
    })) as PublicKeyCredential;
  } catch (x: any) {
    if (cancelled(x)) return false;
    if (x?.name === "InvalidStateError") throw new Error(t("อุปกรณ์นี้มี passkey ของบัญชีนี้อยู่แล้ว"));
    throw x;
  }
  const r = cred.response as AuthenticatorAttestationResponse;
  await api(`/api/passkey/register/finish?sid=${encodeURIComponent(sid)}&name=${encodeURIComponent(name)}`, {
    method: "POST",
    json: {
      id: cred.id, rawId: b64u.enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: b64u.enc(r.clientDataJSON), attestationObject: b64u.enc(r.attestationObject),
        transports: r.getTransports?.() || [],
      },
    },
  });
  return true;
}

// PocketBase's own admin (/_/) has no passkey login. It keeps its session in localStorage under
// "__pb_superusers__" + its path ("/_"), as {token, record} (ui/src/pb.js, LocalAuthStore), on this
// same origin. So a superuser signed in here, by passkey or password, opens the admin already
// signed in: refresh this token for a fresh one and the record, store it there, then open /_/.
export async function openAdmin() {
  const w = window.open("about:blank", "_blank");
  try {
    const j = await api<{ token: string; record: any }>("/api/collections/_superusers/auth-refresh", { method: "POST" });
    localStorage.setItem("__pb_superusers__/_", JSON.stringify({ token: j.token, record: j.record }));
  } catch {
    /* not a superuser or expired: the admin shows its own login */
  }
  if (w) w.location.href = "/_/";
  else location.href = "/_/";
}
