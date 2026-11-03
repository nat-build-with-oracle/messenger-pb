#!/system/bin/sh
# Magisk late_start service: run busybox crond with /data/adb/cron/crontabs (root's table).
# Install: pb/android-cron/install.sh. Log of the jobs: /data/adb/cron/rotate.log.
sleep 30
/data/adb/magisk/busybox crond -b -c /data/adb/cron/crontabs -L /data/adb/cron/crond.log
