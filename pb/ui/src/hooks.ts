import { useEffect, useRef, useState } from "preact/hooks";

// Re-run a loader every `ms` while the tab is visible; keep the last good value on errors.
// `reload` runs it now (the refresh buttons); `loading` is true while a run is in flight.
export function usePoll<T>(load: () => Promise<T>, deps: unknown[], ms = 5000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  const depKey = JSON.stringify(deps), lastKey = useRef(depKey);
  useEffect(() => {
    let live = true;
    if (lastKey.current !== depKey) (lastKey.current = depKey), setData(null); // a reload keeps what is on screen
    const run = () => {
      setLoading(true);
      loadRef.current().then(
        (d) => live && (setData(d), setError("")),
        (e) => live && setError(e.message),
      ).finally(() => live && setLoading(false));
    };
    run();
    const t = setInterval(() => document.visibilityState === "visible" && run(), ms);
    return () => ((live = false), clearInterval(t));
  }, [depKey, tick]);
  return { data, error, loading, reload: () => setTick((n) => n + 1) };
}

export function useHash() {
  const [hash, setHash] = useState(location.hash);
  useEffect(() => {
    const on = () => setHash(location.hash);
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  }, []);
  return hash;
}
