#!/bin/bash
# OmniCloud - single production instance (API 10858, Tracker 10859)
# Usage: ./run.sh start | stop | status | logs

cd "$(dirname "$0")"
LOG_FILE="${LOG_FILE:-$PWD/omnicloud.log}"
PID_FILE="${PID_FILE:-$PWD/omnicloud.pid}"

case "${1:-}" in
  start)
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
      echo "OmniCloud already running (PID $(cat "$PID_FILE"))"
      exit 1
    fi
    echo "Starting OmniCloud (log: $LOG_FILE)..."
    (
      while true; do
        OMNICLOUD_LOG_FILE="$LOG_FILE" ./bin/omnicloud >> "$LOG_FILE" 2>&1
        EXIT=$?
        echo "$(date -Iseconds) OmniCloud exited with $EXIT; restarting in 2s..." >> "$LOG_FILE"
        sleep 2
      done
    ) &
    echo $! > "$PID_FILE"
    echo "Started (restart loop PID $!)"
    ;;
  stop)
    if [ -f "$PID_FILE" ]; then
      PID=$(cat "$PID_FILE")
      kill "$PID" 2>/dev/null && echo "Stopped OmniCloud (PID $PID)" || true
      rm -f "$PID_FILE"
    fi
    pkill -f "./bin/omnicloud" 2>/dev/null || true
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
