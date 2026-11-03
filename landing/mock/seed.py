#!/usr/bin/env python3
"""Seed a LOCAL messenger-pb with made-up data for the landing page screenshots (spec: Nexus, 2026-10-29).

    python3 landing/mock/seed.py http://127.0.0.1:8099 demo@example.com <password> /tmp/neo-mock/line

Every value is fake: names are invented, ids start with 9000000000000, the Discord webhook points at the
.invalid TLD (it can never resolve, so nothing is ever posted), phones are 080-000-000x. Never run this
against the phone's instance.
"""
import json
import os
import sqlite3
import sys
import time
import urllib.request

BASE, EMAIL, PASSWORD, LINE_DIR = sys.argv[1:5]
NOW = int(time.time() * 1000)
DAY, HOUR, MIN = 86_400_000, 3_600_000, 60_000


def api(method, path, body=None, token=None):
    req = urllib.request.Request(BASE + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"content-type": "application/json", **({"Authorization": token} if token else {})})
    with urllib.request.urlopen(req) as r:
        return json.loads(r.read() or b"{}")


TOKEN = api("POST", "/api/collections/_superusers/auth-with-password",
            {"identity": EMAIL, "password": PASSWORD})["token"]


def create(col, rec):
    return api("POST", f"/api/collections/{col}/records", rec, TOKEN)


CONTACTS = {
    "9000000000000001": "Somchai Mockdee (สมชาย ม็อกดี)",
    "9000000000000002": "Malee Testsuk (มาลี เทสสุข)",
    "9000000000000003": "Anan Fakeboon (อนันต์ เฟคบุญ)",
    "9000000000000004": "Ploy Demo (พลอย เดโม่)",
    "9000000000000005": "Kitti Sample (กิตติ แซมเปิล)",
    "9000000000000006": "Nok Placeholder (นก เพลสโฮลเดอร์)",
    "9000000000000007": "Ton Example (ต้น เอ็กแซมเปิล)",
    "9000000000000008": "Fah Mocksiri (ฟ้า ม็อกศิริ)",
}
OWNER = ("9000000000000999", "ร้านเดโม่ (เรา)")
for fbid, name in CONTACTS.items():
    create("contacts", {"fbid": fbid, "name": name})

# thread key, name, group, waiting(unanswered), messages: (minutes ago, sender fbid, text, kind)
THREADS = [
    ("9000000000000101", "ร้านกาแฟดอยม็อก (Doi Mock Coffee)", True, False, [
        (2 * 24 * 60 + 30, "9000000000000003", "สวัสดีครับ ขอสั่งเมล็ดกาแฟคั่วกลาง 5 กก. ครับ", "text"),
        (2 * 24 * 60 + 25, OWNER[0], "ได้ครับ คั่วกลาง 5 กก. 2,250 บาท ส่งวันพฤหัสนะครับ", "text"),
        (2 * 24 * 60 + 20, "9000000000000005", "ขอเพิ่มคั่วเข้ม 2 กก. ด้วยครับ", "text"),
        (2 * 24 * 60 + 10, OWNER[0], "รับทราบครับ รวม 7 กก. 3,150 บาท", "text"),
        (24 * 60 + 300, "9000000000000003", "โอนแล้วนะครับ", "text"),
        (24 * 60 + 290, "9000000000000003", "[รูปภาพ] สลิปโอนเงิน (ภาพตัวอย่าง)", "media"),
        (24 * 60 + 200, OWNER[0], "ได้รับแล้วครับ ขอบคุณครับ", "text"),
        (24 * 60 + 60, "9000000000000006", "พรุ่งนี้มีโปรเมล็ดดริปไหมคะ", "text"),
        (24 * 60 + 40, OWNER[0], "มีครับ ลด 10% ทุกถุง ถึงวันอาทิตย์", "text"),
        (300, "9000000000000005", "ของถึงแล้วครับ หอมมาก", "text"),
        (280, "9000000000000003", "[รูปภาพ] ถุงกาแฟ Doi Mock (ภาพตัวอย่าง)", "media"),
        (250, OWNER[0], "ยินดีครับ ฝากรีวิวด้วยนะครับ", "text"),
        (90, "9000000000000006", "ขอสั่งเพิ่มอีก 3 กก. ส่งวันศุกร์ได้ไหมคะ", "text"),
        (60, OWNER[0], "ได้ครับ จัดส่งแล้วครับ เลขพัสดุ MOCK123456TH", "text"),
        (20, "9000000000000005", "👍", "text"),
    ]),
    ("9000000000000102", "Somchai Mockdee (สมชาย ม็อกดี)", False, True, [
        (26 * 60, "9000000000000001", "สวัสดีครับ ขอใบเสนอราคากาแฟ 1 กก. ครับ", "text"),
        (25 * 60, OWNER[0], "ได้ครับ เดี๋ยวส่งให้ครับ", "text"),
        (3 * 60, "9000000000000001", "ขอราคาส่งถ้าสั่ง 10 กก. ด้วยครับ", "text"),
        (2 * 60, "9000000000000001", "รอคำตอบนะครับ", "text"),
    ]),
    ("9000000000000103", "ทีมช่าง Demo Fix", True, False, [
        (2 * 24 * 60, "9000000000000007", "งานพรุ่งนี้เริ่มกี่โมงครับ", "text"),
        (2 * 24 * 60 - 10, OWNER[0], "9 โมงครับ ที่หน้างานซอยเดโม่ 3", "text"),
        (24 * 60 + 100, "9000000000000008", "[ไฟล์] แบบหน้างาน.pdf (ไฟล์ตัวอย่าง)", "media"),
        (24 * 60 + 90, "9000000000000007", "รับทราบครับ", "text"),
        (400, "9000000000000008", "อุปกรณ์ครบแล้วค่ะ", "text"),
    ]),
    ("9000000000000104", "Malee Testsuk (มาลี เทสสุข)", False, True, [
        (5 * 60, "9000000000000002", "ขอบคุณสำหรับกาแฟค่ะ อร่อยมาก", "text"),
        (40, "9000000000000002", "มีถุงเล็ก 250 กรัมไหมคะ", "text"),
    ]),
]
mid = 0
for key, name, group, waiting, msgs in THREADS:
    last = msgs[-1]
    th = create("threads", {"key": key, "name": name, "is_group": group, "last_ts": NOW - last[0] * MIN,
                            "preview": last[2][:80], "waiting": waiting, "folder": "inbox"})
    for ago, sender, text, kind in msgs:
        mid += 1
        create("messages", {"mid": f"mid.mock.{mid:04d}", "thread": th["id"], "thread_key": key,
                            "ts": NOW - ago * MIN, "sender": sender,
                            "sender_name": OWNER[1] if sender == OWNER[0] else CONTACTS.get(sender, sender),
                            "text": text, "kind": kind, "source": "msys"})
    if waiting:
        create("seen", {"thread_key": key, "contact": msgs[0][1], "name": CONTACTS[msgs[0][1]],
                        "read_ts": NOW - 50 * MIN, "delivered_ts": NOW - 45 * MIN})

create("aliases", {"platform": "Messenger", "key": "9000000000000102", "alias": "ลูกค้าประจำ-สมชาย", "by": "demo"})

# send queue: one draft per state, with its events
SENDS = [
    ("pending", False, "Somchai Mockdee (สมชาย ม็อกดี)", "9000000000000102", "ราคากาแฟ 1 กก. 450 บาท ส่งฟรีครับ", []),
    ("pending", False, "Malee Testsuk (มาลี เทสสุข)", "9000000000000104", "ยินดีครับ ขอบคุณที่อุดหนุน มีถุง 250 กรัม 140 บาทครับ", []),
    ("approved", False, "ทีมช่าง Demo Fix", "9000000000000103", "พรุ่งนี้ 9 โมงเข้าหน้างานครับ", ["approved"]),
    ("sent", False, "ร้านกาแฟดอยม็อก (Doi Mock Coffee)", "9000000000000101", "จัดส่งแล้วครับ เลขพัสดุ MOCK123456TH", ["approved", "sent"]),
    ("rejected", False, "Somchai Mockdee (สมชาย ม็อกดี)", "9000000000000102", "ราคาส่ง 10 กก. 3,500 บาทครับ", ["rejected"]),
    ("sent", True, "Ploy Demo (พลอย เดโม่)", "9000000000000105", "ขอบคุณที่แวะมาร้านครับ", ["approved", "sent"]),
]
for status, archived, chat, key, body, events in SENDS:
    rec = {"platform": "Messenger", "chat": chat, "thread_key": key, "message": body, "rev": 1,
           "note": "ร่างโดย agent (ตัวอย่าง)", "status": status, "archived": archived}
    if status == "sent":
        rec["sent_at"] = time.strftime("%Y-%m-%d %H:%M:%S.000Z", time.gmtime(time.time() - 3600))
    # a new draft always starts pending (a hook enforces it); walk it to its state like a reviewer would
    s = create("sends", dict(rec, status="pending", sent_at=None) if "sent_at" in rec else dict(rec, status="pending"))
    for step in {"approved": ["approved"], "sent": ["approved", "sent"], "rejected": ["rejected"]}.get(status, []):
        body = {"status": step}
        if step == "sent":
            body["sent_at"] = rec["sent_at"]
        api("PATCH", f"/api/collections/sends/records/{s['id']}", body, TOKEN)
    create("send_events", {"send": s["id"], "kind": "queued", "by": "agent@example.com", "rev": 1, "body": body})
    for k in events:
        ev = {"send": s["id"], "kind": k, "by": "demo@example.com" if k != "sent" else "sender@example.com", "rev": 1}
        if k == "rejected":
            ev["comment"] = "ราคาผิด ราคาส่ง 10 กก. คือ 3,800 บาท"
        create("send_events", ev)

# Discord forward: .invalid can never resolve, so nothing is ever posted
fw = create("forwards", {"name": "ร้านกาแฟดอยม็อก (Doi Mock Coffee)", "platform": "Messenger",
                         "thread_key": "9000000000000101", "format": "discord", "enabled": True,
                         "webhook": "https://discord.invalid/api/webhooks/100000000000000001/MOCK-TOKEN-NOT-REAL?thread_id=100000000000000003"})
for i in range(1, 6):
    create("forward_log", {"forward": fw["id"], "mid": f"mid.mock.{i:04d}", "hash": f"mockhash{i:04d}",
                           "status": "sent", "http": 204})
create("forward_log", {"forward": fw["id"], "mid": "mid.mock.0006", "hash": "mockhash0006", "status": "failed",
                       "http": 404, "error": "404 Unknown Webhook (mock)"})

# users: a viewer and an agent (the superuser demo@example.com already exists)
for email, role in (("viewer@example.com", "viewer"), ("agent@example.com", "agent")):
    create("users", {"email": email, "password": "mock-pass-123", "passwordConfirm": "mock-pass-123",
                     "role": role, "name": role.capitalize() + " (demo)", "verified": True})

# LINE: two tiny sqlite files shaped like the LINE app's (only the columns line.go reads)
os.makedirs(LINE_DIR, exist_ok=True)
c = sqlite3.connect(os.path.join(LINE_DIR, "contact"))
c.executescript("drop table if exists contacts; create table contacts(mid text, overridden_name text, profile_name text);")
c.executemany("insert into contacts values (?,?,?)", [
    ("Umock0001", "", "Ploy Demo (พลอย เดโม่)"), ("Umock0002", "", "Kitti Sample (กิตติ แซมเปิล)"),
    ("Umock0003", "", "Fah Mocksiri (ฟ้า ม็อกศิริ)")])
c.commit(); c.close()
l = sqlite3.connect(os.path.join(LINE_DIR, "naver_line"))
l.executescript("""drop table if exists chat; drop table if exists chat_history; drop table if exists groups;
create table chat(chat_id text, chat_name text, type integer, last_from_mid text, last_message text, last_created_time text);
create table chat_history(id integer primary key, chat_id text, from_mid text, type integer, attachement_type integer,
  content text, parameter text, location_name text, created_time text);
create table groups(id text, name text);""")
line_chats = [
    ("Cmock0001", "กลุ่มลูกค้า Mock Garden", 1, [
        (180, "Umock0002", "ต้นไม้ชุดใหม่มาถึงยังครับ"), (170, "", "มาแล้วครับ ส่งรูปให้ดูนะครับ"),
        (160, "", "[รูปภาพ] ต้นมอนสเตอร่า (ภาพตัวอย่าง)"), (120, "Umock0003", "ขอจอง 2 ต้นค่ะ"),
        (100, "", "รับทราบครับ จองให้แล้ว"), (60, "Umock0002", "ส่งวันเสาร์ได้ไหมครับ"),
        (55, "", "ได้ครับ"), (30, "Umock0003", "ขอบคุณค่ะ"), (20, "Umock0002", "👍"), (10, "", "ยินดีครับ")]),
    ("Umock0001", "Ploy Demo (พลอย เดโม่)", 0, [
        (300, "Umock0001", "สวัสดีค่ะ ร้านเปิดกี่โมงคะ"), (290, "", "9 โมงถึง 6 โมงเย็นครับ"),
        (200, "Umock0001", "วันอาทิตย์เปิดไหมคะ"), (190, "", "เปิดครับ"), (90, "Umock0001", "ขอบคุณค่ะ")]),
]
hid = 0
for cid, name, typ, msgs in line_chats:
    for ago, frm, text in msgs:
        hid += 1
        l.execute("insert into chat_history values (?,?,?,?,?,?,?,?,?)",
                  (hid, cid, frm, 1, 0, text, "", "", str(NOW - ago * MIN)))
    last = msgs[-1]
    l.execute("insert into chat values (?,?,?,?,?,?)", (cid, name, typ, last[1], last[2], str(NOW - last[0] * MIN)))
    if typ == 1:
        l.execute("insert into groups values (?,?)", (cid, name))
l.commit(); l.close()
print("seeded: 8 contacts, 4 threads, 26 messages, 6 sends, 1 forward + 6 log rows, 2 users, 2 LINE chats")
