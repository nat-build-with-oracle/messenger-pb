# /// script
# requires-python = ">=3.11"
# dependencies = ["lancedb>=0.20", "pyarrow>=17", "requests>=2.32"]
# ///
"""Semantic search over the archive: Messenger + LINE text, embedded on m5's ANE, kept in LanceDB on
a RustFS server (s3://messenger-archive/lance).

    uv run pb/embed/embed.py sync            # embed what is new (first run: everything)
    uv run pb/embed/embed.py search "gogoboard workshop" [-n 10] [--platform LINE] [--json]
    uv run pb/embed/embed.py --model bge-m3 sync   # second table, embedded on gb300 (ssh tunnel :11534)
    uv run pb/embed/embed.py fts-index           # ICU keyword index (Thai word segmentation)
    uv run pb/embed/embed.py compare "q1" "q2" …   # top hits of every model with a table, side by side

Reads the archive through the phone's API as the agent account (~/.config/messenger-pb/sq-agent.env),
the S3 key from ~/.config/messenger-pb/s3.env, else pass (rustfs/messenger-pb-key).
Hourly on m5: launchd agent com.laris.messenger-embed (pb/embed/install-launchd.sh). Nothing is written to the phone.
Default: gemma2 + hybrid search (ICU keyword and vectors, reciprocal rank fusion), chosen by `compare`
on 2026-10-25 (6 queries, 4 methods, 22,606 rows each). Models (PROFILES): gemma = embeddinggemma on m5's ANE (127.0.0.1:11435), with its query/document prompts,
table "messages"; bge-m3 = bge-m3 in gb300's Ollama through `ssh -L 11534:127.0.0.1:11434 gb300`, no
prompts, table "messages_bge_m3"; gemma2 = embeddinggemma2 on the same ANE server, table
"messages_gemma2". compare also runs keyword search: LanceDB full-text with the ICU tokenizer (Thai words,
LanceDB >= 0.38) over the text of the model's table (`fts-index` builds it; sync rebuilds it). Vectors of different models are never mixed: one table each.
"""
import argparse, os, subprocess, sys, time
import requests, pyarrow as pa, lancedb

URI = os.environ.get("LANCE_URI", "s3://messenger-archive/lance")
PROFILES = {
    "gemma": {"url": os.environ.get("ANE_URL", "http://127.0.0.1:11435"), "model": "embeddinggemma:ane-w8a16", "dim": 768,
              "table": "messages", "query": "task: search result | query: ", "doc": "title: none | text: "},
    "gemma2": {"url": os.environ.get("ANE_URL", "http://127.0.0.1:11435"), "model": "embeddinggemma2:ane-w16", "dim": 768,
               "table": "messages_gemma2", "query": "task: search result | query: ", "doc": "title: none | text: "},
    "bge-m3": {"url": os.environ.get("BGE_URL", "http://127.0.0.1:11534"), "model": "bge-m3", "dim": 1024,
               "table": "messages_bge_m3", "query": "", "doc": ""},
}
P = PROFILES["gemma"]


def env_file(path):
    out = {}
    for line in open(os.path.expanduser(path)):
        if "=" in line and not line.lstrip().startswith("#"):
            k, v = line.strip().split("=", 1)
            out[k] = v.strip().strip('"').strip("'")
    return out


def s3_options():
    f = os.path.expanduser("~/.config/messenger-pb/s3.env")  # launchd: pass may need a pinentry
    if os.path.exists(f):
        e = env_file(f)
        key, secret = e["S3_ACCESS_KEY"], e["S3_SECRET"]
    else:
        key, secret = subprocess.run(["pass", "show", "rustfs/messenger-pb-key"],
                                     capture_output=True, text=True, check=True).stdout.splitlines()[:2]
    return {"aws_access_key_id": key, "aws_secret_access_key": secret,
            "aws_endpoint": os.environ.get("S3_ENDPOINT", "http://s3.example.ts.net:9200"),
            "aws_region": "rustfs", "allow_http": "true",
            "aws_virtual_hosted_style_request": "false"}


def api():
    cfg = env_file("~/.config/messenger-pb/sq-agent.env")
    base = cfg["SQ_API"].rstrip("/")
    s = requests.Session()
    r = s.post(f"{base}/api/collections/users/auth-with-password",
               json={"identity": cfg["SQ_AGENT_EMAIL"], "password": cfg["SQ_AGENT_PASSWORD"]}, timeout=30)
    r.raise_for_status()
    s.headers["Authorization"] = r.json()["token"]
    return base, s


def embed(texts, query=False):
    pre = P["query"] if query else P["doc"]
    r = requests.post(f"{P['url']}/api/embed", json={"model": P["model"], "input": [pre + t[:2000] for t in texts]}, timeout=300)
    r.raise_for_status()
    return r.json()["embeddings"]


def messenger_rows(base, s):
    names, page = {}, 1
    while True:
        j = s.get(f"{base}/api/collections/threads/records", params={"page": page, "perPage": 500, "fields": "key,name"}, timeout=60).json()
        names.update({t["key"]: t["name"] for t in j["items"]})
        if page >= j["totalPages"]:
            break
        page += 1
    page = 1
    while True:
        j = s.get(f"{base}/api/collections/messages/records", params={
            "page": page, "perPage": 500, "sort": "ts", "skipTotal": "1", "filter": "kind='text' && text!=''",
            "fields": "mid,thread_key,ts,sender_name,text"}, timeout=120).json()
        for m in j["items"]:
            yield {"id": "m:" + m["mid"], "platform": "Messenger", "chat": m["thread_key"],
                   "chat_name": names.get(m["thread_key"], ""), "sender": m["sender_name"], "ts": m["ts"], "text": m["text"]}
        if len(j["items"]) < 500:
            break
        page += 1


def line_rows(base, s):
    for c in s.get(f"{base}/api/line/chats", timeout=60).json():
        j = s.get(f"{base}/api/line/chats/{c['id']}", params={"limit": 5000}, timeout=120).json()
        for m in j["items"]:
            if m["kind"] == "text" and m["text"].strip():
                yield {"id": f"l:{c['id']}:{m['id']}", "platform": "LINE", "chat": c["id"], "chat_name": c["name"],
                       "sender": "Nat" if m["me"] else m["name"], "ts": m["ts"], "text": m["text"]}


def schema():
    return pa.schema([("id", pa.string()), ("platform", pa.string()), ("chat", pa.string()), ("chat_name", pa.string()),
                      ("sender", pa.string()), ("ts", pa.int64()), ("text", pa.string()),
                      ("vector", pa.list_(pa.float32(), P["dim"]))])


def open_table(create=False):
    db = lancedb.connect(URI, storage_options=s3_options())
    if P["table"] in db.list_tables().tables:
        return db.open_table(P["table"])
    return db.create_table(P["table"], schema=schema()) if create else None


def sync(batch):
    t0 = time.time()
    base, s = api()
    tbl = open_table(create=True)
    n = tbl.count_rows()
    have = set(tbl.search().select(["id"]).limit(n).to_arrow()["id"].to_pylist()) if n else set()
    new = [r for r in list(messenger_rows(base, s)) + list(line_rows(base, s)) if r["id"] not in have]
    print(f"archive read in {time.time() - t0:.0f}s: {len(have)} already embedded, {len(new)} new", flush=True)
    done = 0
    for i in range(0, len(new), batch):
        part = new[i:i + batch]
        for r, v in zip(part, embed([r["text"] for r in part])):
            r["vector"] = v
        tbl.add(pa.Table.from_pylist(part, schema=schema()))
        done += len(part)
        if done % (batch * 20) == 0 or done == len(new):
            print(f"  {done}/{len(new)}  {done / (time.time() - t0):.0f}/s", flush=True)
    if done or not any(i.index_type == "FTS" for i in tbl.list_indices()):
        fts_index(tbl)  # 1 s for the whole table: rebuilt so new rows are found by keyword too
    print(f"sync done: +{done} rows, table has {tbl.count_rows()} in {time.time() - t0:.0f}s")


def hits(q, n, platform, mode="vector"):
    """mode: vector (meaning), fts (ICU keyword), hybrid (both, merged by reciprocal rank fusion)."""
    tbl = open_table()
    if tbl is None:
        sys.exit(f"no table {P['table']} yet: run sync first")
    if mode == "fts":
        qry = tbl.search(q, query_type="fts").limit(n)
    elif mode == "hybrid":
        from lancedb.rerankers import RRFReranker
        qry = (tbl.search(query_type="hybrid").vector(embed([q], query=True)[0]).text(q)
               .distance_type("cosine").rerank(RRFReranker()).limit(n))
    else:
        qry = tbl.search(embed([q], query=True)[0]).metric("cosine").limit(n)
    if platform:
        qry = qry.where(f"platform = '{platform}'")
    return qry.to_list()


def fts_index(tbl=None):
    tbl = tbl or open_table()
    t0 = time.time()
    tbl.create_fts_index("text", replace=True, base_tokenizer="icu", with_position=False,
                         lower_case=True, stem=False, remove_stop_words=False, ascii_folding=True)
    print(f"ICU full-text index on {P['table']}.text: {tbl.count_rows()} rows in {time.time() - t0:.0f}s")


def score(r):
    if "_relevance_score" in r:
        return f"{r['_relevance_score']:.4f}"
    if "_score" in r:
        return f"{r['_score']:6.2f}"
    return f"{1 - r['_distance']:.3f}"


def show(name, rows):
    print(f"  [{name}]")
    for r in rows:
        sc = score(r)
        print(f"    {sc}  [{r['platform'][:4]}] {r['chat_name'][:22]} - {r['sender'][:14]} - {r['text'][:90]!r}")


def compare(queries, n):
    global P
    for q in queries:
        print(f"\n=== {q}")
        P = PROFILES["gemma2"]
        for mode in ("fts", "hybrid"):
            try:
                show("icu keyword" if mode == "fts" else "hybrid icu+gemma2", hits(q, n, None, mode))
            except Exception as e:
                print(f"  [{mode}] unavailable: {str(e)[:120]}")
        for name, prof in PROFILES.items():
            P = prof
            try:
                rows = hits(q, n, None)
            except (SystemExit, requests.RequestException) as e:
                print(f"  [{name}] unavailable: {e}")
                continue
            show(name, rows)


def search(q, n, platform, as_json=False, mode="hybrid"):
    rows = hits(q, n, platform, mode)
    if as_json:
        import json
        print(json.dumps([{"score": float(score(r)), "time": time.strftime("%d/%m/%Y %H:%M", time.localtime(r["ts"] / 1000)),
                           **{k: r[k] for k in ("platform", "chat", "chat_name", "sender", "text")}} for r in rows], ensure_ascii=False))
        return
    for r in rows:
        when = time.strftime("%d/%m/%Y %H:%M", time.localtime(r["ts"] / 1000))
        print(f"{score(r)}  {when}  [{r['platform']}] {r['chat_name']} - {r['sender']} - {r['text'][:200]!r}")


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", choices=list(PROFILES), default="gemma2")
    sub = ap.add_subparsers(dest="cmd", required=True)
    sp = sub.add_parser("sync"); sp.add_argument("--batch", type=int, default=64)
    sq = sub.add_parser("search"); sq.add_argument("query"); sq.add_argument("-n", type=int, default=10)
    sq.add_argument("--platform", choices=["Messenger", "LINE"])
    sq.add_argument("--json", action="store_true")
    sq.add_argument("--mode", choices=["hybrid", "vector", "fts"], default="hybrid")
    sub.add_parser("fts-index")
    sc = sub.add_parser("compare"); sc.add_argument("queries", nargs="+"); sc.add_argument("-n", type=int, default=5)
    a = ap.parse_args()
    P = PROFILES[a.model]
    if a.cmd == "sync":
        sync(a.batch)
    elif a.cmd == "fts-index":
        fts_index()
    elif a.cmd == "compare":
        compare(a.queries, a.n)
    else:
        search(a.query, a.n, a.platform, a.json, a.mode)
