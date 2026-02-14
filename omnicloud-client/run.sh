#!/bin/bash
# OmniCloud Client Instance Runner
# This runs a client instance for testing client-main communication

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

BINARY="$SCRIPT_DIR/bin/omnicloud"
CONFIG="$SCRIPT_DIR/auth.config"
LOG="$SCRIPT_DIR/omnicloud-client.log"
PID_FILE="$SCRIPT_DIR/omnicloud-client.pid"

start() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "Client already running (PID $PID)"
            return 1
        fi
        rm -f "$PID_FILE"
    fi

    echo "Starting OmniCloud Client (log: $LOG)..."
    
    # Run in background with restart loop
    (
        while true; do
            "$BINARY" -config "$CONFIG" >> "$LOG" 2>&1
            EXIT_CODE=$?
            if [ $EXIT_CODE -eq 0 ]; then
                echo "$(date): Client exited cleanly" >> "$LOG"
                break
            fi
            echo "$(date): Client exited with code $EXIT_CODE, restarting in 5s..." >> "$LOG"
            sleep 5
        done
    ) &
    
    LOOP_PID=$!
    echo $LOOP_PID > "$PID_FILE"
    echo "Started (restart loop PID $LOOP_PID)"
}

stop() {
    if [ ! -f "$PID_FILE" ]; then
        echo "Client not running (no PID file)"
        return 1
    fi
    
    PID=$(cat "$PID_FILE")
    if kill -0 "$PID" 2>/dev/null; then
        # Kill the restart loop
        kill "$PID" 2>/dev/null
        
        # Also kill any omnicloud processes started by this script
        pkill -f "$BINARY -config $CONFIG" 2>/dev/null
        
        echo "Stopped OmniCloud Client (PID $PID)"
    else
        echo "Client not running (stale PID file)"
    fi
    rm -f "$PID_FILE"
}

status() {
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            echo "Client running (PID $PID)"
            echo "Log: $LOG"
            echo ""
            echo "Last 10 log lines:"
            tail -10 "$LOG" 2>/dev/null
            return 0
        fi
    fi
    echo "Client not running"
    return 1
}

logs() {
    tail -f "$LOG"
}

case "$1" in
    start)
        start
        ;;
    stop)
        stop
        ;;
    restart)
        stop
        sleep 2
        start
        ;;
    status)
        status
        ;;
    logs)
        logs
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status|logs}"
        exit 1
        ;;
esac
