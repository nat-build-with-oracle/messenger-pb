#!/bin/zsh
# LINE twin of messenger-scroll-to-top.sh: scroll an open LINE chat on a rooted Android phone
# to its first message, so the LINE app downloads the history into its own database
# (naver_line, table chat_history), where messenger-pb's LINE tab reads it live.
#
#   line-scroll-to-top.sh <adb-serial> <chat-id>        (open the chat on the phone first)
#
# Same lessons as the Messenger one: slow swipes with pauses, progress = the chat's row count,
# stop only after 60 swipes with nothing new, then LOOK at the screen. Opening a chat marks it read.
set -u
S=${1:?adb serial over tailscale, e.g. 100.64.0.2:5555}
C=${2:?LINE chat id, e.g. u…/c… (chat.chat_id)}
SQLITE=${SQLITE:-/data/local/tmp/sqlite3}
DB=/data/data/jp.naver.line.android/databases/naver_line

q() { adb -s "$S" shell "su -c '$SQLITE \"file:$DB?mode=ro\" \"select count(*), min(cast(created_time as integer)) from chat_history where chat_id=\\\"$C\\\";\"'" | tr -d '\r'; }
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
