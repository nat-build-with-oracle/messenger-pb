#!/bin/zsh
# The reply desk on m5 under launchd: `just desk INTERVAL` from this checkout, started at login and
# restarted when it exits. FileVault is on, so every boot stops for Nat's password and that login starts
# gui-domain agents; no LaunchDaemon needed. `pass` works here without a shell (tested 2026-10-30, #56).
# Log: ~/Library/Logs/reply-desk.log (past 1 MB the desk keeps its newest half). Remove: launchctl bootout gui/$UID/com.laris.reply-desk
set -eu
L=com.laris.reply-desk
P=~/Library/LaunchAgents/$L.plist
REPO=${0:A:h:h:h}
INTERVAL=${1:-20}
cat > $P <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- messenger-archive #56: the "AI ร่าง" reply desk. Installed by pb/reply-desk/install-launchd.sh. -->
<plist version="1.0"><dict>
  <key>Label</key><string>$L</string>
  <key>ProgramArguments</key><array>
    <string>/opt/homebrew/bin/just</string><string>desk</string><string>$INTERVAL</string>
  </array>
  <key>WorkingDirectory</key><string>$REPO</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>StandardOutPath</key><string>$HOME/Library/Logs/reply-desk.log</string>
  <key>StandardErrorPath</key><string>$HOME/Library/Logs/reply-desk.log</string>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>REPLY_DESK_LOG</key><string>$HOME/Library/Logs/reply-desk.log</string>
  </dict>
</dict></plist>
PLIST
plutil -lint -s $P
launchctl bootout gui/$UID/$L 2>/dev/null || true
launchctl bootstrap gui/$UID $P
launchctl print gui/$UID/$L | grep -E "^\s+(state|pid|working directory) =" | head -3
