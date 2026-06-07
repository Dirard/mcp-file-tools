#!/usr/bin/env sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
EXE="$ROOT/mcp-file-tools"
LOG_DIR="$ROOT/logs"
SERVER_LOG="$LOG_DIR/mcp-file-tools.log"
WATCHDOG_LOG="$LOG_DIR/watchdog.log"
HTTP_ADDR="${MCP_HTTP_ADDR:-127.0.0.1:8787}"

mkdir -p "$LOG_DIR"

write_watchdog_log() {
    printf '%s %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$1" >> "$WATCHDOG_LOG"
}

write_watchdog_log "watchdog started root=$ROOT exe=$EXE http=$HTTP_ADDR"

while true; do
    if [ ! -x "$EXE" ]; then
        write_watchdog_log "executable not found or not executable; retrying in 10s"
        sleep 10
        continue
    fi

    write_watchdog_log "starting mcp-file-tools"
    if "$EXE" --http "$HTTP_ADDR" --log-file "$SERVER_LOG"; then
        exit_code=0
    else
        exit_code=$?
    fi
    write_watchdog_log "mcp-file-tools exited exit_code=$exit_code; restarting in 3s"
    sleep 3
done
