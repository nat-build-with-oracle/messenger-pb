// The one layout every page shares: a list pane on the left, the open item on the right
// (one at a time on a phone), each list with a title that goes back home and a refresh button.
import type { ComponentChildren } from "preact";
import { useEffect, useState } from "preact/hooks";
import { t } from "./i18n";

export function TwoPane({ open, list, detail }: { open: boolean; list: ComponentChildren; detail: ComponentChildren }) {
  return (
    <div class="grid min-h-0 flex-1 gap-4 p-4 md:grid-cols-[minmax(320px,400px)_1fr] max-md:gap-0 max-md:p-0">
      <section class={`flex min-h-0 flex-col overflow-hidden rounded-xl bg-panel max-md:rounded-none ${open ? "max-md:hidden" : ""}`}>{list}</section>
      <section class={`flex min-h-0 flex-col overflow-hidden rounded-xl bg-panel max-md:rounded-none ${open ? "" : "max-md:hidden"}`}>{detail}</section>
    </div>
  );
}

export function PaneTitle({ title, home, refresh, loading }: { title: string; home: () => void; refresh: () => void; loading?: boolean }) {
  return (
    <div class="mx-4 mb-2.5 mt-3.5 flex items-center gap-2">
      <h2 class="flex-1 truncate text-[22px] font-bold">
        <button onClick={home} title={t("กลับหน้าแรกของหน้านี้")} class="hover:text-mine">{title}</button>
      </h2>
      <Refresh onClick={refresh} loading={loading} />
    </div>
  );
}

// Spins only after a click, until that load finishes (the 5-second background polls stay quiet).
export function Refresh({ onClick, loading }: { onClick: () => void; loading?: boolean }) {
  const [pressed, setPressed] = useState(false);
  useEffect(() => void (!loading && setPressed(false)), [loading]);
  return (
    <button onClick={() => (setPressed(true), onClick())} title={t("รีเฟรช")} aria-label={t("รีเฟรช")}
      class="grid size-9 flex-none place-items-center rounded-full bg-raised text-muted hover:bg-raised-hi hover:text-ink">
      <svg viewBox="0 0 24 24" class={`size-[18px] ${pressed ? "animate-spin" : ""}`} fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <path d="M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7" />
      </svg>
    </button>
  );
}

export const Empty = ({ children }: { children: ComponentChildren }) => <p class="m-auto px-4 py-12 text-center text-muted">{children}</p>;

// The resting state of a detail pane: a soft mark, what to do, and one hint. Fills the pane so the
// two columns weigh the same before anything is open.
export function Resting({ title, hint, icon }: { title: string; hint?: string; icon: string }) {
  return (
    <div class="m-auto grid max-w-[320px] justify-items-center gap-3 px-6 py-12 text-center">
      <span class="grid size-16 place-items-center rounded-full bg-amber/10 text-amber ring-8 ring-amber/[0.04]">
        <svg viewBox="0 0 24 24" class="size-7" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d={icon} /></svg>
      </span>
      <p class="text-[15px] font-medium text-ink">{title}</p>
      {hint && <p class="text-[13px] leading-relaxed text-muted">{hint}</p>}
    </div>
  );
}
export const ICON_CHAT = "M12 4c-4.7 0-8.5 3.1-8.5 7 0 2.1 1.1 4 2.9 5.3L6 20l3.6-1.8c.8.2 1.6.3 2.4.3 4.7 0 8.5-3.1 8.5-7S16.7 4 12 4z M8.5 11h.01M12 11h.01M15.5 11h.01";
export const ICON_LIST = "M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01";
