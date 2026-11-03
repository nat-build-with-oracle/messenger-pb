#!/bin/zsh
# Scroll an open Messenger chat on a rooted Android phone to its very first message, so the
# Messenger app downloads the whole history into its own database (msys) — where an archive
# that reads msys (like messenger-pb's sync) can pick it up. Nothing is read off the screen:
# the app does the loading, this only keeps it scrolling and measures progress in the db.
#
#   messenger-scroll-to-top.sh <adb-serial> <messenger-user-id> <thread-key>
#
# Open the chat on the phone first. Needs root (su) and a sqlite3 binary on the phone.
#
# What we learned getting here (2026-10-24):
#   - Fast flings stall: past a point the app stops fetching and the screen sits on a loading
#     spinner. Slow swipes with short pauses keep it loading.
#   - Progress is the chat's message COUNT in msys, not what is on screen. The oldest date can
#     stay put for a while (an early system row) while the count climbs.
#   - "Nothing new for a few rounds" is not the top on a slow network: stop only after a long
#     stretch (12 checks = 60 swipes) with the count unchanged, then look at the screen.
#   - Opening a chat marks it read for everyone in it.
set -u
S=${1:?adb serial over tailscale, e.g. 100.64.0.2:5555}
UID_=${2:?your Messenger user id (the number in msys_database_<id>)}
T=${3:?thread key of the chat}
SQLITE=${SQLITE:-/data/local/tmp/sqlite3}
DB="/data/data/com.facebook.orca/databases/msys_database_$UID_"

q() { adb -s "$S" shell "su -c '$SQLITE \"file:$DB?mode=ro\" \"select count(*), min(timestamp_ms) from messages where thread_key=$T;\"'" | tr -d '\r'; }
when() { local ts=${1#*|}; date -r $((ts / 1000)) '+%Y-%m-%d %H:%M' 2>/dev/null; }

prev="" same=0 i=0
echo "start: $(q)"
while [ $same -lt 12 ] && [ $i -lt 5000 ]; do
  for k in 1 2 3 4 5; do adb -s "$S" shell input swipe 540 700 540 1700 600; sleep 1; done
  sleep 2
  i=$((i + 5)); cur=$(q)
  echo "swipes $i  count|oldest = $cur  ($(when "$cur"))"
  if [ "$cur" = "$prev" ]; then same=$((same + 1)); else same=0; fi
  prev=$cur
done
echo "stopped after $i swipes: $(q) — now look at the screen: is the start of the chat showing?"
