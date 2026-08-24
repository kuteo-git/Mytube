#!/usr/bin/env bash
# Install (or reinstall) the login agent that keeps the stack running.
#
#   scripts/install-agent.sh          install and start
#   scripts/install-agent.sh remove   stop and uninstall
#
# A LaunchAgent, not a LaunchDaemon: this needs the user's session — $HOME for
# yt-dlp's cookies and the speech server, and the external volume, which is not
# mounted for a daemon starting before login.
set -euo pipefail

LABEL="com.luke.local-mytube"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
LOG_DIR="${TMPDIR:-/tmp}/local-youtube"
TARGET="gui/$(id -u)/$LABEL"

if [ "${1:-}" = "remove" ]; then
  launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
  rm -f "$PLIST"
  echo "removed $LABEL"
  exit 0
fi

mkdir -p "$LOG_DIR"
cat > "$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-lc</string>
    <string>exec $REPO/scripts/boot.sh</string>
  </array>
  <key>WorkingDirectory</key><string>$REPO</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>StandardOutPath</key><string>$LOG_DIR/boot.log</string>
  <key>StandardErrorPath</key><string>$LOG_DIR/boot.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/opt/homebrew/bin:/opt/homebrew/opt/postgresql@17/bin:/usr/bin:/bin:/usr/sbin:/sbin:$HOME/.local/bin</string>
  </dict>
</dict>
</plist>
PLIST_EOF

launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
launchctl enable "$TARGET"
echo "installed $LABEL"
echo "  status:  launchctl print $TARGET | head"
echo "  log:     $LOG_DIR/boot.log   (and http://localhost:8184)"
