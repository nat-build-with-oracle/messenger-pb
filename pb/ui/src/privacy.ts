// Demo mode: the same screens with nothing private on them, for screenshots and screen sharing.
// Every name becomes a made-up person or group and every message a made-up line of about the same
// length and language (demo-data.ts), picked by a hash so the same person and line stay the same
// while the screen is shared; pictures are blurred, avatar photos dropped, the email hidden. It changes only what this browser shows; the data and
// the server are untouched. Stored per browser ("sq.privacy"); switching reloads the page.
import { GROUPS, PEOPLE, pick, simulateLine } from "./demo-data";

export const privacy = localStorage.getItem("sq.privacy") === "on";
document.documentElement.dataset.privacy = privacy ? "on" : "off";

export function setPrivacy(on: boolean) {
  localStorage.setItem("sq.privacy", on ? "on" : "off");
  location.reload();
}

const hash = (s: string) => [...s].reduce((x, c) => (x * 31 + c.codePointAt(0)!) >>> 0, 7);

// A person or chat name → a made-up one. Keyed on the name itself, so the avatar, the list and the
// header all agree. A name that reads like a group (a list of people, or a long title) gets a group.
export const pvName = (name: string, seed?: string) => {
  if (!privacy || !name) return name;
  const h = hash(name || seed || "");
  return /,|\+/.test(name) || [...name].length > 28 ? pick(GROUPS, h) : pick(PEOPLE, h);
};
// Free text → a made-up line of about the same length and language.
export const pvText = (s: string | undefined) => (privacy && s ? simulateLine(s, hash(s)) : s || "");
export const pvEmail = (e: string) => (privacy ? "demo@example.com" : e);
