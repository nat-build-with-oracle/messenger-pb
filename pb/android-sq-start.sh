#!/system/bin/sh
# send-queue + chat view on the phone: Tailscale address only, archive sync from the live
# Messenger dbs (signed-in reads only, see migrations/1792070000), no MQTT.
# Install:  adb push android-sq-start.sh /data/local/tmp/sq/start.sh   (binary at /data/local/tmp/sq/messenger-pb)
# Restart:  su -c 'sh /data/local/tmp/sq/start.sh'
#
# --dev=false: PocketBase treats a binary under os.TempDir() as `go run` and turns dev mode on,
# and on Android os.TempDir() is /data/local/tmp — dev mode logs every SQL statement
# (password hashes, token keys, draft text) into serve.log.
# The old server is found by its executable, never by a command-line pattern: `pkill -f <path>`
# also matched the su command that ran this script and killed it.
# setsid: without a new session the adb shell that ran this never returns.
T=/data/local/tmp/sq
# After a new binary is moved into place the old process's exe reads "<path> (deleted)".
for p in /proc/[0-9]*; do
  case "$(readlink $p/exe 2>/dev/null)" in
    "$T/messenger-pb"|"$T/messenger-pb (deleted)") kill ${p#/proc/} && echo "stopped ${p#/proc/}" ;;
  esac
done
sleep 1
cd $T || exit 1
# The chat tab's pictures come from the native server's cache (sqmedia.go → 127.0.0.1:8722).
# Start it if it is not running; it binds the phone's loopback only and is left alone otherwise.
N=/data/local/tmp/messenger
if [ -x $N ]; then
  up=""
  for p in /proc/[0-9]*; do [ "$(readlink $p/exe 2>/dev/null)" = "$N" ] && up=1; done
  [ -z "$up" ] && setsid nohup $N serve 127.0.0.1:8722 > /data/local/tmp/messenger-serve.log 2>&1 < /dev/null &
fi
# Passkeys (passkey.go) need HTTPS with a valid certificate: m5 fronts this server with
# `tailscale serve --bg --https=8447 http://100.64.0.2:8097`, so the RP is m5's tailnet name.
SQ_BASE=http://phone.example.ts.net:8097 \
PASSKEY_RP_ID=mac.example.ts.net PASSKEY_ORIGIN=https://mac.example.ts.net:8447 \
  setsid nohup $T/messenger-pb --dir $T/pb_data serve --dev=false --http 100.64.0.2:8097 > $T/serve.log 2>&1 < /dev/null &
sleep 1
for p in /proc/[0-9]*; do
  [ "$(readlink $p/exe 2>/dev/null)" = "$T/messenger-pb" ] && echo "running ${p#/proc/}"
done
exit 0
