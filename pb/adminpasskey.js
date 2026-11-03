// "Sign in with passkey" on PocketBase's admin login (/_/#/login). Served by adminpasskey.go, which
// adds this file to the admin's index.html. Plain JS, no build: it only watches for the login form.
// The ceremony is the review page's (loginWithPasskey in ui/src/api.ts) with ?only=_superusers.

const KEY = "__pb_superusers__" + location.pathname.replace(/\/$/, ""); // the admin's LocalAuthStore key
const ID = "messenger-pb-passkey";

const dec = (s) => Uint8Array.from(atob(s.replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, "")), (c) => c.charCodeAt(0));
const enc = (b) => (b ? btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "") : null);

async function post(path, body) {
  const r = await fetch(path, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(j.message || "HTTP " + r.status), { status: r.status });
  return j;
}

async function signIn() {
  let begun;
  try {
    begun = await post("/api/passkey/login/begin");
  } catch (x) {
    if (x.status === 503) throw new Error("Passkeys are not set up at this address. Open the https address (tailscale serve) or localhost, or use the password.");
    throw x;
  }
  const pk = begun.options.publicKey;
  let cred;
  try {
    cred = await navigator.credentials.get({
      publicKey: { ...pk, challenge: dec(pk.challenge), allowCredentials: pk.allowCredentials?.map((c) => ({ ...c, id: dec(c.id) })) },
    });
  } catch (x) {
    if (x?.name === "NotAllowedError" || x?.name === "AbortError") throw new Error("Cancelled, or Touch ID / Face ID / PIN did not pass.");
    if (x?.name === "SecurityError") throw new Error("Passkeys do not work at this address. Open the https address or localhost.");
    throw x;
  }
  const r = cred.response;
  let j;
  try {
    j = await post(`/api/passkey/login/finish?sid=${encodeURIComponent(begun.sid)}&only=_superusers`, {
      id: cred.id, rawId: enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: enc(r.clientDataJSON), authenticatorData: enc(r.authenticatorData),
        signature: enc(r.signature), userHandle: enc(r.userHandle),
      },
    });
  } catch (x) {
    if (x.status === 403) throw new Error("This passkey belongs to a reviewer account. The admin takes superuser passkeys only.");
    throw new Error("This passkey could not sign in. Use the password.");
  }
  if (j.record?.collectionName !== "_superusers") throw new Error("This passkey belongs to a reviewer account. The admin takes superuser passkeys only.");
  localStorage.setItem(KEY, JSON.stringify({ token: j.token, record: j.record }));
  location.hash = "#/";
  location.reload();
}

function addButton(form) {
  const box = document.createElement("div");
  box.id = ID;
  box.className = "block m-t-sm";
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "btn lg block secondary";
  btn.innerHTML = '<i class="ri-fingerprint-line" aria-hidden="true"></i><span class="txt">Sign in with passkey · เข้าด้วย passkey</span>';
  const msg = document.createElement("div");
  msg.className = "txt-center txt-sm txt-danger m-t-5";
  msg.setAttribute("role", "alert");
  btn.onclick = async () => {
    btn.disabled = true;
    btn.classList.add("loading");
    msg.textContent = "";
    try {
      await signIn();
    } catch (x) {
      msg.textContent = x.message || String(x);
    }
    btn.disabled = false;
    btn.classList.remove("loading");
  };
  box.append(btn, msg);
  form.after(box);
}

// WebAuthn needs a secure context (https, or http://localhost): elsewhere the button would only fail.
if (window.isSecureContext && typeof window.PublicKeyCredential === "function") {
  const check = () => {
    // the admin re-renders its forms (MFA step, route change): keep the button beside the live one
    const form = document.querySelector("form.auth-with-password-form");
    const box = document.getElementById(ID);
    if (box && box.previousElementSibling === form) return;
    box?.remove();
    if (form) addButton(form);
  };
  new MutationObserver(check).observe(document.documentElement, { childList: true, subtree: true });
  check();
}
