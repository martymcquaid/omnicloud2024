#!/bin/bash
# OmniCloud - single production instance (API 10858, Tracker 10851, Torrent data 10852)
# Usage: ./run.sh start | stop | status | logs

cd "$(dirname "$0")"
LOG_FILE="${LOG_FILE:-$PWD/omnicloud.log}"
PID_FILE="${PID_FILE:-$PWD/omnicloud.pid}"

# Ports this instance uses (avoid "address already in use" on restart)
API_PORT="${API_PORT:-10858}"
TRACKER_PORT="${TRACKER_PORT:-10851}"
TORRENT_PORT="${TORRENT_PORT:-10852}"

# Wait until a port is no longer in use (so restarts can bind). Optional env: OMNICLOUD_STOP_TIMEOUT (default 15).
wait_for_port_free() {
  local port=$1
  local max=${OMNICLOUD_STOP_TIMEOUT:-15}
  while [ "$max" -gt 0 ]; do
    if ! ss -tlnp 2>/dev/null | grep -q ":${port} "; then
      return 0
    fi
    sleep 1
    max=$((max - 1))
  done
  echo "Warning: port $port still in use after ${OMNICLOUD_STOP_TIMEOUT:-15}s" >&2
  return 1
}

case "${1:-}" in
  start)
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "OmniCloud already running (PID $(cat "$PID_FILE"))"
      exit 1
    fi
    echo "Starting OmniCloud (log: $LOG_FILE)..."
    (
      while true; do
        # Do not set OMNICLOUD_LOG_FILE here: the app would write to both stdout and that file,
        # and we redirect stdout to LOG_FILE, so every line would appear twice.
        ./bin/omnicloud >> "$LOG_FILE" 2>&1
        EXIT=$?
        echo "$(date -Iseconds) OmniCloud exited with $EXIT; restarting in 5s..." >> "$LOG_FILE"
        sleep 5
      done
    ) &
    echo $! > "$PID_FILE"
    echo "Started (restart loop PID $!)"
    ;;
  stop)
    if [ -f "$PID_FILE" ]; then
      PID=$(cat "$PID_FILE")
      # Kill the restart-loop wrapper AND its child processes
      kill -- -"$PID" 2>/dev/null || kill "$PID" 2>/dev/null || true
      echo "Stopped OmniCloud restart loop (PID $PID)"
      rm -f "$PID_FILE"
    fi
    # Kill ALL omnicloud server processes (but not the client)
    pkill -f "./bin/omnicloud$" 2>/dev/null || true
    # Give processes time to exit gracefully
    sleep 2
    # Force-kill any stragglers
    pkill -9 -f "./bin/omnicloud$" 2>/dev/null || true
    sleep 1
    wait_for_port_free "$TRACKER_PORT" || true
    wait_for_port_free "$API_PORT" || true
    ;;
  status)
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "OmniCloud running (PID $(cat "$PID_FILE"))"
      curl -sS http://localhost:10858/api/v1/health | head -1
    else
      echo "OmniCloud not running"
    fi
    ;;
  logs)
    tail -f "$LOG_FILE"
    ;;
  *)
    echo "Usage: $0 start | stop | status | logs"
    exit 1
    ;;
esac
