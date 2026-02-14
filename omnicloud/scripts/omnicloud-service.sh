#!/bin/bash
# OmniCloud – one script for build, release package, and service (start/stop/restart)
# Usage: ./scripts/omnicloud-service.sh <command>
# Commands: build | release | start | stop | restart | status | logs | all

set -e
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ROOT"

LOG_FILE="${OMNICLOUD_LOG_FILE:-$ROOT/omnicloud.log}"
PID_FILE="${OMNICLOUD_PID_FILE:-$ROOT/omnicloud.pid}"
BIN="$ROOT/bin/omnicloud"

# ---- helpers ----
do_build() {
  echo "[build] Building binary..."
  make build
  echo "[build] Done: $BIN"
}

do_release() {
  echo "[release] Creating upgrade package..."
  "$SCRIPT_DIR/build-release.sh"
  echo "[release] Done"
}

do_stop() {
  if [ -f "$PID_FILE" ]; then
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
      kill "$PID" 2>/dev/null && echo "[stop] Stopped (PID $PID)" || true
    fi
    rm -f "$PID_FILE"
  fi
  pkill -f "bin/omnicloud" 2>/dev/null || true
  echo "[stop] OmniCloud stopped"
}

do_start() {
  if [ ! -f "$BIN" ]; then
    echo "[start] Binary missing, building..."
    do_build
  fi
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "[start] OmniCloud already running (PID $(cat "$PID_FILE"))"
    return 0
  fi
  echo "[start] Starting OmniCloud (log: $LOG_FILE)..."
  (
    while true; do
      OMNICLOUD_LOG_FILE="$LOG_FILE" "$BIN" >> "$LOG_FILE" 2>&1
      EXIT=$?
      echo "$(date -Iseconds) OmniCloud exited with $EXIT; restarting in 2s..." >> "$LOG_FILE"
      sleep 2
    done
  ) &
  echo $! > "$PID_FILE"
  echo "[start] Started (restart loop PID $(cat "$PID_FILE"))"
}

do_status() {
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "OmniCloud running (PID $(cat "$PID_FILE"))"
    curl -sf http://localhost:10858/api/v1/health >/dev/null && echo "API: OK" || echo "API: not responding"
  else
    echo "OmniCloud not running"
  fi
}

do_logs() {
  tail -f "$LOG_FILE"
}

do_restart() {
  do_stop
  sleep 1
  do_build
  do_start
}

do_all() {
  echo "=== OmniCloud: build + release + restart ==="
  do_stop
  sleep 1
  do_build
  do_release
  do_start
  echo "=== Done ==="
  do_status
}

# ---- main ----
case "${1:-}" in
  build)   do_build ;;
  release) do_release ;;
  start)   do_start ;;
  stop)    do_stop ;;
  restart) do_restart ;;
  status)  do_status ;;
  logs)    do_logs ;;
  all)     do_all ;;
  *)
    echo "Usage: $0 <command>"
    echo ""
    echo "Commands:"
    echo "  build    - build binary (make build)"
    echo "  release  - create upgrade package (build-release.sh)"
    echo "  start    - build if needed, then start (restart loop)"
    echo "  stop     - stop OmniCloud"
    echo "  restart  - stop, build, start"
    echo "  status   - show run status and API health"
    echo "  logs     - tail log file"
    echo "  all      - stop, build, release, start (full deploy)"
    exit 1
    ;;
esac
