#!/usr/bin/env bash
# Liberated Stream Deck - macOS launchd agent installer
#
#   bash install/macos/install.sh            install / repair the LaunchAgent
#   bash install/macos/install.sh status     show current state
#   bash install/macos/install.sh uninstall  stop and remove the LaunchAgent
#
# What it does:
#   1. builds bin/deckdemo from the current checkout
#   2. writes ~/Library/LaunchAgents/<label>.plist (RunAtLoad + KeepAlive)
#   3. loads it via launchctl bootstrap (gui domain)
#   4. waits briefly and prints the launchd state + daemon log tail
#
# Paths are derived from this script's location, so it works from any clone
# under any username. The deck will NOT be drawn unless the daemon's remote
# controller answers "run_hardware_demo" (see the README), so install
# works headless too; rendering is a separate concern.

set -euo pipefail

LABEL="com.mikebirdtech.liberated-stream-deck"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN="$REPO_ROOT/bin/deckdemo"
PLIST_DEST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_DIR="$HOME/Library/Logs/liberated-stream-deck"

printf_info()  { printf '\033[1;34m[i]\033[0m %s\n' "$*"; }
printf_ok()    { printf '\033[1;32m[ok]\033[0m %s\n' "$*"; }
printf_err()   { printf '\033[1;31m[!]\033[0m %s\n' "$*" >&2; }

die() { printf_err "$*"; exit 1; }

usage() {
    sed -n '2,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 0
}

build() {
    printf_info "building ${BIN}"
    (cd "$REPO_ROOT" && go build -o bin/deckdemo ./cmd/deckdemo)
    printf_ok "build complete"
}

write_plist() {
    printf_info "writing $PLIST_DEST"
    mkdir -p "$LOG_DIR"
    cat > "$PLIST_DEST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${LABEL}</string>
	<key>ProgramArguments</key>
	<array>
		<string>${BIN}</string>
	</array>
	<key>WorkingDirectory</key>
	<string>${REPO_ROOT}</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>${LOG_DIR}/out.log</string>
	<key>StandardErrorPath</key>
	<string>${LOG_DIR}/err.log</string>
</dict>
</plist>
EOF
    plutil -lint "$PLIST_DEST" >/dev/null || die "plist failed lint"
    printf_ok "plist written and validated"
}

stop_agent() {
    if launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
        launchctl bootout "gui/$(id -u)/$LABEL" || true
        printf_ok "stopped existing agent"
    fi
}

start_agent() {
    printf_info "loading agent"
    launchctl bootstrap "gui/$(id -u)" "$PLIST_DEST" || die "launchctl bootstrap failed"
    sleep 2
}

show_status() {
    echo
    printf_ok "launchd state:"
    launchctl print "gui/$(id -u)/$LABEL" 2>/dev/null \
        | grep -E 'state =|pid =|last exit code|program =' \
        || printf_err "agent not loaded (run: bash $0)"
    echo
    printf_ok "running processes:"
    pgrep -fl deckdemo || true
    if [ -f "$LOG_DIR/err.log" ]; then
        echo
        printf_ok "daemon log tail:"
        tail -8 "$LOG_DIR/err.log"
    fi
}

cmd_install() {
    build
    write_plist
    stop_agent
    start_agent
    show_status
}

cmd_uninstall() {
    stop_agent
    rm -f "$PLIST_DEST"
    rmdir "$LOG_DIR" 2>/dev/null || true
    printf_ok "agent removed"
}

case "${1:-install}" in
    install)   cmd_install ;;
    status)    show_status ;;
    uninstall) cmd_uninstall ;;
    -h|--help) usage ;;
    *) die "unknown command: $1 (use install|status|uninstall)" ;;
esac
