#!/bin/zsh
# Install the phone's native cron: busybox crond started at boot by Magisk, running rotate.sh
# every 15 minutes. Needs `ssh android-a14` (root over the tailnet). Safe to re-run.
set -eu
H=${PHONE_SSH:-android-a14}
D=${0:A:h}
ssh $H 'mkdir -p /data/adb/cron/crontabs'
ssh $H "cat > /data/adb/cron/rotate.sh" < $D/rotate.sh
ssh $H "cat > /data/adb/service.d/crond.sh" < $D/crond.sh
ssh $H 'chmod 755 /data/adb/cron/rotate.sh /data/adb/service.d/crond.sh
echo "*/15 * * * * /data/adb/cron/rotate.sh" > /data/adb/cron/crontabs/root
for p in $(pidof busybox); do grep -q crond /proc/$p/cmdline && kill $p; done
sleep 1
/data/adb/magisk/busybox crond -b -c /data/adb/cron/crontabs -L /data/adb/cron/crond.log
for p in $(pidof busybox); do grep -q crond /proc/$p/cmdline && echo "crond running, pid $p"; done
cat /data/adb/cron/crontabs/root'
