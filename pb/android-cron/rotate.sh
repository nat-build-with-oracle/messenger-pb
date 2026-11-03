#!/system/bin/sh
# Every 15 minutes (busybox crond, see crond.sh): put LINE in front on its chat list at :00 and
# :30, Messenger on its inbox at :15 and :45, and leave it there. An app in front stays awake and
# keeps syncing its database, which the archive reads.
#
# Never a chat: an open chat is a read receipt to the other person. LINE is opened by its chat-list
# link. Messenger has no such link that comes to the front, so it is launched, and it restores its
# last screen: leave Messenger on its inbox. No Back here: uiautomator's dump of Messenger includes a
# hidden chat view under the inbox (2026-10-25), so a check built on it presses Back by mistake,
# and Back on the inbox closes the app.
# Skipped while someone is using the phone: any other app in front.
# crond gives a bare environment; take the system's from zygote (am, input, dumpsys expect it).
for kv in $(tr '\0' '\n' < /proc/$(pidof zygote64)/environ); do export "$kv"; done
LOG=/data/adb/cron/rotate.log
# One line per run, key=value after the time, read by the app's Logs tab (GET /api/phone/cronlog):
#   2026-10-29 15:00:06 +0700 app=line ok=no before=NotificationShade after=NotificationShade vpn=up pb=up
# ok=yes|no|skip says whether the run did its job (the app is in front afterwards), so a run that
# only tried is never mistaken for one that worked. vpn = the phone holds its tailnet address;
# pb = messenger-pb answers on it. Lines older than this format ("line: ...") are read too.
log() { echo "$(date '+%F %T %z') $*" >> $LOG; }
focus() { dumpsys window 2>/dev/null | grep -m1 mCurrentFocus; }
short() { sed 's/.* u0 //; s/}.*//; s/.*mCurrentFocus=null.*/none/; s/ /_/g'; }
# The app in front. On Samsung the NotificationShade window holds key focus (mCurrentFocus) while
# the screen is off, dozing or not yet fully on, so the run's result is read from the focused app
# (mFocusedApp), not from the focused window (2026-10-29: LINE in front, mCurrentFocus=shade).
front() { dumpsys window 2>/dev/null | grep -m1 mFocusedApp | sed 's/.* u0 //; s/ t[0-9]*}.*//; s/}.*//'; }
# Only a tun interface counts: mobile data can also hand out a 100.64/10 (carrier NAT) address.
ts=$(for i in $(ls /sys/class/net | grep '^tun'); do ip -4 addr show $i 2>/dev/null; done | grep -o 'inet 100\.[0-9.]*' | head -1 | cut -d' ' -f2)
vpn=down; pb=down
if [ -n "$ts" ]; then
  vpn=up
  /data/adb/magisk/busybox nc -z -w 2 "$ts" 8097 2>/dev/null && pb=up
fi
f=$(focus)
before=$(echo "$f" | short)
awake() { dumpsys power 2>/dev/null | grep -q 'mWakefulness=Awake'; }
locked() { dumpsys window 2>/dev/null | grep -q 'isKeyguardShowing=true'; }
# Someone is using the phone only when it is awake, unlocked and another app is in front.
if awake && ! locked; then
  case "$f" in
    *com.sec.android.app.launcher*|*jp.naver.line.android*|*com.facebook.orca*|*NotificationShade*|"") ;;
    *) log "app=- ok=skip before=$before note=phone-in-use vpn=$vpn pb=$pb"; exit 0 ;;
  esac
fi
# A dark, locked phone keeps nothing in front (155 runs in a row on 2026-10-28/29 ended on the
# notification shade). So: wake it, drop the lock screen (swipe only, no PIN) and the shade, do the
# run, and put the screen back to sleep if this run woke it. The screen is on for about 10 s a run,
# not all the time, so the phone does not heat up.
# From a long doze the screen takes a while to come on, and a dismiss sent before it is fully on
# is dropped (15:45 and 16:00 on 2026-10-29 stayed locked; a wake after a short sleep worked). So
# wait for the screen, then dismiss and check, up to three times, with an up-swipe as the fallback.
on() { dumpsys window 2>/dev/null | grep -q 'mScreenOnFully=true'; }
woke=no
if ! awake; then
  input keyevent KEYCODE_WAKEUP; woke=yes
  n=0; while ! on && [ $n -lt 10 ]; do sleep 0.5; n=$((n+1)); done
fi
n=0
while locked && [ $n -lt 3 ]; do
  wm dismiss-keyguard >/dev/null 2>&1; sleep 1.5
  locked && input swipe 540 1800 540 500 200 && sleep 1
  n=$((n+1))
done
lock=off; locked && lock=on
cmd statusbar collapse >/dev/null 2>&1
sleep 1
rest() { [ "$woke" = yes ] && input keyevent KEYCODE_SLEEP; }

slot=$(( $(date +%M | sed 's/^0//') / 15 % 2 ))
if [ "$slot" = 0 ]; then
  am start -a android.intent.action.VIEW -d line://nv/chat jp.naver.line.android >/dev/null 2>&1
  sleep 6
  f=$(focus)
  note=""
  case "$f" in *ChatHistoryActivity*) input keyevent KEYCODE_BACK; sleep 1; note=" note=was-on-a-chat-pressed-back" ;; esac
  after=$(focus | short); app=$(front)
  case "$app" in jp.naver.line.android*) ok=yes ;; *) ok=no ;; esac
  rest
  log "app=line ok=$ok before=$before after=$after front=$app vpn=$vpn pb=$pb woke=$woke lock=$lock$note"
else
  am start -n com.facebook.orca/.auth.StartScreenActivity >/dev/null 2>&1
  sleep 6
  after=$(focus | short); app=$(front)
  case "$app" in com.facebook.orca*) ok=yes ;; *) ok=no ;; esac
  rest
  log "app=messenger ok=$ok before=$before after=$after front=$app vpn=$vpn pb=$pb woke=$woke lock=$lock"
fi
# keep the log short
tail -n 500 $LOG > $LOG.tmp && mv $LOG.tmp $LOG
