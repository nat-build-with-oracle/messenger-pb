// Made-up people and lines for demo mode (privacy.ts). Written for this file, on the kinds of topics
// the real chats cover (coffee orders, brewing, maker and IoT work, AI tools, events, shipping), so a
// shared screen looks like a working inbox. Nothing here comes from a real conversation.

export const PEOPLE = [
  "Ploy Saengthong", "Krit Wongsa", "Nok Chaiyaporn", "Mint Rattana", "Beam Sutthisak", "Fon Kanchana",
  "Arm Pongsakorn", "Pim Thanaporn", "Tong Jirawat", "Bee Natcha", "Game Thanawat", "Aom Siriporn",
  "Boss Kittipong", "Gift Pornpimol", "Nut Chanin", "Ice Wipawee", "Top Supachai", "Fern Nattaya",
  "Pete Anuwat", "May Kornkanok", "พี่ต้น", "น้องแพร", "พี่หนุ่ม", "คุณจอย", "พี่โอ๊ต", "น้องมิว",
  "คุณแอน", "พี่บอล", "น้องเฟิร์ส", "พี่ตาล", "Alex Turner", "Sara Lim", "Kenji Mori", "Daniel Park",
];

export const GROUPS = [
  "ทีม Maker Lab เชียงใหม่", "Coffee Roasters Club", "Brew Day ทีมหมัก", "IoT Sensor Project",
  "AI Builders TH", "Expo Booth 2026", "ร้านกาแฟดอยสูง", "Workshop Weekend", "ทีมส่งของ North",
  "Night Market Crew", "Hackathon ทีม B", "Board Game Friday",
];

export const LINES_TH = [
  "ได้ครับ", "โอเคค่ะ", "ขอบคุณครับ 🙏", "เดี๋ยวเช็คให้นะ", "ถึงแล้วครับ", "รับทราบค่ะ", "จัดไป", "พรุ่งนี้นะ",
  "ส่งของรอบบ่ายได้ไหมคะ", "เมล็ดคั่วกลางเหลือกี่ถุงครับ", "รอบนี้ขอ 2 กิโลนะคะ", "โอนแล้วนะครับ เดี๋ยวส่งสลิปให้",
  "เซนเซอร์ตัวใหม่ค่าเริ่มนิ่งแล้วครับ", "บอร์ดรีบูตเองตอนตีสาม ลองดู log ให้หน่อย", "พรุ่งนี้เข้า lab กี่โมงครับ",
  "เบียร์ล็อตใหม่หมักครบ 14 วันแล้ว", "วัดค่า gravity ได้ 1.012 ครับ", "ขอรูปหน้างานหน่อยค่ะ",
  "ลองให้ AI ช่วยร่างแล้ว อ่านดูก่อนนะ", "release ใหม่ขึ้นแล้ว ลองอัปเดตดูครับ", "ประชุมเลื่อนเป็นบ่ายสองนะคะ",
  "บูธเราอยู่โซน B ใกล้ทางเข้าครับ", "ของถึงแล้วค่ะ แพ็คมาดีมาก", "คิวคั่วสัปดาห์หน้าเต็มแล้วครับ",
  "เดี๋ยวขอเวลาสรุปตัวเลขให้ก่อนเย็นนี้", "ใครว่างช่วยยกโต๊ะเข้าห้องหน่อย", "ส่งไฟล์ล่าสุดให้ในไดรฟ์แล้วค่ะ",
  "สรุปว่าเอาแบบที่สองนะครับ ดูง่ายกว่า", "ลูกค้าถามราคาส่ง 10 กิโลค่ะ", "งานนี้ขอเลื่อนไปวันจันทร์ได้ไหมครับ",
  "เมื่อวานฝนตกหนัก สถานีวัดน้ำส่งค่าครบทุกจุด", "ลองต่อจอใหม่แล้ว ภาพคมขึ้นเยอะเลย",
  "ถ้าโอเคเดี๋ยวพรุ่งนี้เริ่มทำเลยนะครับ จะได้ทันงานวันเสาร์", "ขอบคุณที่มาช่วยกันนะทุกคน งานวันนี้ผ่านไปด้วยดีมาก",
];

export const LINES_EN = [
  "Sounds good", "Thanks!", "On my way", "Got it 👍", "Let me check", "Perfect", "See you tomorrow", "Done ✅",
  "Can we move the call to 3pm?", "Shipping the order this afternoon", "The new sensor batch looks stable",
  "Pushed the fix, please pull and try again", "Booth setup starts at 8am", "Draft is in the shared folder",
  "Roast profile v2 tastes much better", "Could you send the invoice again?", "Gravity reading is 1.012 today",
  "The demo went really well, thanks everyone", "Let's ship the smaller version first and iterate",
  "I tried the AI draft, it needs a shorter intro", "Workshop seats are almost full",
];

const FAKE_LINK = "https://example.com/doc/demo";

// Same input → same pick, so a screen stays stable while it is shared.
export const pick = <T,>(list: T[], h: number) => list[h % list.length];

// A line of about the same length and language as the original.
export function simulateLine(s: string, h: number): string {
  if (!/[\p{L}\p{N}]/u.test(s)) return s; // only emoji / punctuation: nothing private, keep it
  if (/^https?:\/\//.test(s.trim())) return FAKE_LINK;
  const pool = /[฀-๿]/.test(s) ? LINES_TH : LINES_EN;
  const len = [...s].length;
  const fit = pool.filter((l) => {
    const n = [...l].length;
    return len < 12 ? n < 16 : len < 45 ? n >= 12 && n < 50 : n >= 30;
  });
  return pick(fit.length ? fit : pool, h);
}
