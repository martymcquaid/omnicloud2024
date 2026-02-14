#!/bin/bash
# OmniCloud Test Environment Controller
# Manages both main server and test client instances

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAIN_DIR="$SCRIPT_DIR/omnicloud"
CLIENT_DIR="$SCRIPT_DIR/omnicloud-client"

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_status() {
    echo ""
    echo "=========================================="
    echo " OmniCloud Test Environment Status"
    echo "=========================================="
    
    # Main server
    echo -e "\n${YELLOW}Main Server (port 10858):${NC}"
    if [ -f "$MAIN_DIR/omnicloud.pid" ]; then
        PID=$(cat "$MAIN_DIR/omnicloud.pid")
        if kill -0 "$PID" 2>/dev/null; then
            echo -e "  Status: ${GREEN}Running${NC} (PID $PID)"
        else
            echo -e "  Status: ${RED}Not running${NC} (stale PID file)"
        fi
    else
        echo -e "  Status: ${RED}Not running${NC}"
    fi
    echo "  URL: http://localhost:10858"
    echo "  Log: $MAIN_DIR/omnicloud.log"
    
    # Test client
    echo -e "\n${YELLOW}Test Client (port 10859):${NC}"
    if [ -f "$CLIENT_DIR/omnicloud-client.pid" ]; then
        PID=$(cat "$CLIENT_DIR/omnicloud-client.pid")
        if kill -0 "$PID" 2>/dev/null; then
            echo -e "  Status: ${GREEN}Running${NC} (PID $PID)"
        else
            echo -e "  Status: ${RED}Not running${NC} (stale PID file)"
        fi
    else
        echo -e "  Status: ${RED}Not running${NC}"
    fi
    echo "  URL: http://localhost:10859"
    echo "  Log: $CLIENT_DIR/omnicloud-client.log"
    
    echo ""
    echo "=========================================="
}

start_all() {
    echo "Starting OmniCloud Test Environment..."
    
    echo -e "\n${YELLOW}Starting Main Server...${NC}"
    cd "$MAIN_DIR" && ./run.sh start
    
    sleep 3
    
    echo -e "\n${YELLOW}Starting Test Client...${NC}"
    cd "$CLIENT_DIR" && ./run.sh start
    
    sleep 3
    print_status
}

stop_all() {
    echo "Stopping OmniCloud Test Environment..."
    
    echo -e "\n${YELLOW}Stopping Test Client...${NC}"
    cd "$CLIENT_DIR" && ./run.sh stop 2>/dev/null || true
    
    echo -e "\n${YELLOW}Stopping Main Server...${NC}"
    cd "$MAIN_DIR" && ./run.sh stop 2>/dev/null || true
    
    print_status
}

restart_all() {
    stop_all
    sleep 2
    start_all
}

logs_main() {
    tail -f "$MAIN_DIR/omnicloud.log"
}

logs_client() {
    tail -f "$CLIENT_DIR/omnicloud-client.log"
}

logs_both() {
    tail -f "$MAIN_DIR/omnicloud.log" "$CLIENT_DIR/omnicloud-client.log"
}

case "$1" in
    start)
        start_all
        ;;
    stop)
        stop_all
        ;;
    restart)
        restart_all
        ;;
    status)
        print_status
        ;;
    logs)
        case "$2" in
            main)
                logs_main
                ;;
            client)
                logs_client
                ;;
            *)
                logs_both
                ;;
        esac
        ;;
    main)
        cd "$MAIN_DIR" && ./run.sh "$2"
        ;;
    client)
        cd "$CLIENT_DIR" && ./run.sh "$2"
        ;;
    *)
        echo "OmniCloud Test Environment Controller"
        echo ""
        echo "Usage: $0 {start|stop|restart|status|logs [main|client]|main <cmd>|client <cmd>}"
        echo ""
        echo "Commands:"
        echo "  start     - Start both main server and test client"
        echo "  stop      - Stop both instances"
        echo "  restart   - Restart both instances"
        echo "  status    - Show status of both instances"
        echo "  logs      - Follow logs from both (or specify: main/client)"
        echo "  main      - Control main server (start|stop|restart|status|logs)"
        echo "  client    - Control test client (start|stop|restart|status|logs)"
        echo ""
        echo "Examples:"
        echo "  $0 start              # Start entire test environment"
        echo "  $0 logs client        # Follow test client logs"
        echo "  $0 main restart       # Restart only the main server"
        exit 1
        ;;
esac
