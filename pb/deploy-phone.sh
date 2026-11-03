#!/bin/zsh
# Build the UI, build the Android binary, push it to the phone and restart the queue.
#   ./deploy-phone.sh            (PHONE=<adb serial> to pick another device)
# adb cannot write into the root-owned sq/ directory, so the binary goes to /data/local/tmp
# first and is moved into place as root; start.sh stops the old server and starts the new one.
set -e
cd "${0:A:h}/ui" && bun install --frozen-lockfile && bun run build
cd .. && CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -ldflags=-s -o /tmp/messenger-pb-android .
S=${PHONE:-100.64.0.2:5555}
adb -s $S push android-sq-start.sh /data/local/tmp/sq-start.sh.new
adb -s $S push /tmp/messenger-pb-android /data/local/tmp/messenger-pb-sq.new
adb -s $S shell "su -c 'mv /data/local/tmp/messenger-pb-sq.new /data/local/tmp/sq/messenger-pb && mv /data/local/tmp/sq-start.sh.new /data/local/tmp/sq/start.sh && chmod 755 /data/local/tmp/sq/messenger-pb && sh /data/local/tmp/sq/start.sh'"
