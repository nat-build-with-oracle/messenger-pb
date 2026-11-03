#!/bin/zsh
# Hourly embed sync on m5 (launchd, at :05): new Messenger + LINE text -> ANE -> LanceDB on RustFS.
# Log: ~/Library/Logs/messenger-embed.log. Remove: launchctl bootout gui/$UID/com.laris.messenger-embed
set -eu
L=com.laris.messenger-embed
P=~/Library/LaunchAgents/$L.plist
REPO=${0:A:h:h:h}
cat > $P <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>$L</string>
  <key>ProgramArguments</key><array>
    <string>/opt/homebrew/bin/uv</string><string>run</string><string>-q</string>
    <string>$REPO/pb/embed/embed.py</string><string>sync</string>
  </array>
  <key>StartCalendarInterval</key><dict><key>Minute</key><integer>5</integer></dict>
  <key>StandardOutPath</key><string>$HOME/Library/Logs/messenger-embed.log</string>
  <key>StandardErrorPath</key><string>$HOME/Library/Logs/messenger-embed.log</string>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>/opt/homebrew/bin:/usr/bin:/bin</string></dict>
</dict></plist>
PLIST
launchctl bootout gui/$UID/$L 2>/dev/null || true
launchctl bootstrap gui/$UID $P
launchctl print gui/$UID/$L | grep -E "state|path" | head -3
