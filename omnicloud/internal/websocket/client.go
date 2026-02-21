package websocket

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/omnicloud/omnicloud/pkg/dcp"
)

// ClientConnector manages the WebSocket connection from client to main server
type ClientConnector struct {
	serverID          uuid.UUID
	serverName        string
	serverLocation    string
	macAddress        string
	mainServerURL     string
	registrationKey   string
	softwareVersion   string
	scanPath          string

	conn              *websocket.Conn
	connMu            sync.RWMutex

	send              chan []byte
	stop              chan struct{}
	connected         bool
	reconnectInterval time.Duration

	db                *sql.DB

	// Command handlers
	onRestart         func() error
	onUpgrade         func(version string) error
	onRescan          func() error
	onStatusRequest   func() map[string]interface{}
	onDeleteContent   func(packageID, packageName, infoHash, targetPath string) (result string, message string, err error)
}

// NewClientConnector creates a new WebSocket client connector
func NewClientConnector(
	db *sql.DB,
	serverID uuid.UUID,
	serverName string,
	serverLocation string,
	mainServerURL string,
	registrationKey string,
	softwareVersion string,
	scanPath string,
) (*ClientConnector, error) {
	macAddress, err := dcp.GetMACAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get MAC address: %w", err)
	}

	return &ClientConnector{
		serverID:          serverID,
		serverName:        serverName,
		serverLocation:    serverLocation,
		macAddress:        macAddress,
		mainServerURL:     mainServerURL,
		registrationKey:   registrationKey,
		softwareVersion:   softwareVersion,
		scanPath:          scanPath,
		send:              make(chan []byte, 256),
		stop:              make(chan struct{}),
		reconnectInterval: 10 * time.Second,
		db:                db,
	}, nil
}

// SetOnRestart sets the handler for restart commands
func (c *ClientConnector) SetOnRestart(handler func() error) {
	c.onRestart = handler
}

// SetOnUpgrade sets the handler for upgrade commands
func (c *ClientConnector) SetOnUpgrade(handler func(version string) error) {
	c.onUpgrade = handler
}

// SetOnRescan sets the handler for rescan commands
func (c *ClientConnector) SetOnRescan(handler func() error) {
	c.onRescan = handler
}

// SetOnStatusRequest sets the handler for status requests
func (c *ClientConnector) SetOnStatusRequest(handler func() map[string]interface{}) {
	c.onStatusRequest = handler
}

// SetOnDeleteContent sets the handler for delete content commands
func (c *ClientConnector) SetOnDeleteContent(handler func(packageID, packageName, infoHash, targetPath string) (string, string, error)) {
	c.onDeleteContent = handler
}

// Start begins the WebSocket client connection
func (c *ClientConnector) Start(ctx context.Context) {
	log.Printf("[WS Client] Starting WebSocket connector to %s", c.mainServerURL)

	// Initial connection
	go c.connect(ctx)

	// Heartbeat loop
	go c.heartbeatLoop(ctx)

	// Handle shutdown
	<-ctx.Done()
	close(c.stop)
	c.disconnect()
}

// connect establishes a WebSocket connection to the main server
func (c *ClientConnector) connect(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		default:
		}

		if err := c.dial(); err != nil {
			log.Printf("[WS Client] Failed to connect: %v, retrying in %v", err, c.reconnectInterval)
			time.Sleep(c.reconnectInterval)
			continue
		}

		// Connection successful, start read/write pumps
		log.Printf("[WS Client] Connected to main server")
		c.connMu.Lock()
		c.connected = true
		c.connMu.Unlock()

		// Send initial heartbeat
		c.sendHeartbeat()

		// Start pumps
		done := make(chan struct{})
		go c.readPump(done)
		go c.writePump(done)

		// Wait for disconnect
		<-done

		c.connMu.Lock()
		c.connected = false
		c.connMu.Unlock()

		log.Printf("[WS Client] Disconnected, reconnecting in %v", c.reconnectInterval)
		time.Sleep(c.reconnectInterval)
	}
}

// dial establishes the WebSocket connection
func (c *ClientConnector) dial() error {
	// Build WebSocket URL
	wsURL := fmt.Sprintf("%s/ws?server_id=%s&mac_address=%s&registration_key=%s",
		c.mainServerURL, c.serverID, c.macAddress, c.registrationKey)

	// Replace http:// with ws://
	wsURL = "ws" + wsURL[4:]

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return err
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	return nil
}

// disconnect closes the WebSocket connection
func (c *ClientConnector) disconnect() {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.connected = false
}

// readPump handles incoming messages from the server
func (c *ClientConnector) readPump(done chan struct{}) {
	defer func() {
		close(done)
		c.disconnect()
	}()

	c.connMu.RLock()
	conn := c.conn
	c.connMu.RUnlock()

	if conn == nil {
		return
	}

	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WS Client] Read error: %v", err)
			}
			return
		}

		c.handleMessage(message)
	}
}

// writePump handles outgoing messages to the server
func (c *ClientConnector) writePump(done chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case <-done:
			return

		case message, ok := <-c.send:
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("[WS Client] Write error: %v", err)
				return
			}

		case <-ticker.C:
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[WS Client] Ping error: %v", err)
				return
			}
		}
	}
}

// handleMessage processes incoming messages from the main server
func (c *ClientConnector) handleMessage(data []byte) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("[WS Client] Failed to parse message: %v", err)
		return
	}

	switch msg.Type {
	case MessageTypePing:
		// Respond with pong
		pong := NewMessage(MessageTypePong, nil)
		if data, err := pong.ToJSON(); err == nil {
			c.send <- data
		}

	case MessageTypeCommand:
		c.handleCommand(data)

	case MessageTypeRequest:
		c.handleRequest(msg)

	default:
		log.Printf("[WS Client] Unknown message type: %s", msg.Type)
	}
}

// handleCommand processes command messages from the server
func (c *ClientConnector) handleCommand(data []byte) {
	var cmd CommandMessage
	if err := json.Unmarshal(data, &cmd); err != nil {
		log.Printf("[WS Client] Failed to parse command: %v", err)
		return
	}

	log.Printf("[WS Client] Received command: %s (ID: %s)", cmd.Command, cmd.MessageID)

	var err error
	var responseMsg string
	var success bool

	switch cmd.Command {
	case CommandRestart:
		responseMsg, success, err = c.handleRestartCommand()

	case CommandUpgrade:
		version, _ := cmd.Payload.(map[string]interface{})["version"].(string)
		responseMsg, success, err = c.handleUpgradeCommand(version)

	case CommandRescan:
		responseMsg, success, err = c.handleRescanCommand()

	case CommandStatusUpdate:
		c.sendHeartbeat()
		responseMsg = "Status updated"
		success = true

	case CommandDeleteContent:
		responseMsg, success, err = c.handleDeleteContentCommand(cmd.Payload)

	default:
		responseMsg = fmt.Sprintf("Unknown command: %s", cmd.Command)
		success = false
	}

	// Send response
	response := NewResponseMessage(cmd.MessageID, success, responseMsg, err, nil)
	if data, err := response.ToJSON(); err == nil {
		c.send <- data
	}
}

// handleRequest processes request messages from the server
func (c *ClientConnector) handleRequest(msg Message) {
	requestType, _ := msg.Payload["type"].(string)

	var payload interface{}
	switch requestType {
	case "status":
		if c.onStatusRequest != nil {
			payload = c.onStatusRequest()
		}
	}

	response := NewResponseMessage(msg.MessageID, true, "Request processed", nil, payload)
	if data, err := response.ToJSON(); err == nil {
		c.send <- data
	}
}

// handleRestartCommand handles restart commands
func (c *ClientConnector) handleRestartCommand() (string, bool, error) {
	log.Printf("[WS Client] Processing restart command")

	if c.onRestart != nil {
		if err := c.onRestart(); err != nil {
			return "Restart failed", false, err
		}
	}

	// Perform restart
	go func() {
		time.Sleep(2 * time.Second)
		log.Printf("[WS Client] Restarting application...")
		syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()

	return "Restart scheduled", true, nil
}

// handleUpgradeCommand handles upgrade commands
func (c *ClientConnector) handleUpgradeCommand(version string) (string, bool, error) {
	log.Printf("[WS Client] Processing upgrade command to version %s", version)

	if c.onUpgrade != nil {
		if err := c.onUpgrade(version); err != nil {
			return "Upgrade failed", false, err
		}
	}

	return fmt.Sprintf("Upgrade to %s scheduled", version), true, nil
}

// handleDeleteContentCommand handles delete content commands
func (c *ClientConnector) handleDeleteContentCommand(payload interface{}) (string, bool, error) {
	log.Printf("[WS Client] Processing delete_content command")

	payloadMap, ok := payload.(map[string]interface{})
	if !ok {
		return "Invalid payload", false, fmt.Errorf("invalid payload format")
	}

	packageID, _ := payloadMap["package_id"].(string)
	packageName, _ := payloadMap["package_name"].(string)
	infoHash, _ := payloadMap["info_hash"].(string)
	targetPath, _ := payloadMap["target_path"].(string)

	if c.onDeleteContent == nil {
		return "Delete content handler not configured", false, fmt.Errorf("no delete handler")
	}

	result, message, err := c.onDeleteContent(packageID, packageName, infoHash, targetPath)
	if err != nil {
		return message, false, err
	}

	return message, result == "deleted", nil
}

// handleRescanCommand handles rescan commands
func (c *ClientConnector) handleRescanCommand() (string, bool, error) {
	log.Printf("[WS Client] Processing rescan command")

	if c.onRescan != nil {
		if err := c.onRescan(); err != nil {
			return "Rescan failed", false, err
		}
	}

	return "Rescan started", true, nil
}

// heartbeatLoop sends periodic heartbeats to the server
func (c *ClientConnector) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stop:
			return
		case <-ticker.C:
			c.connMu.RLock()
			connected := c.connected
			c.connMu.RUnlock()

			if connected {
				c.sendHeartbeat()
			}
		}
	}
}

// sendHeartbeat sends a heartbeat message with current status
func (c *ClientConnector) sendHeartbeat() {
	// Calculate storage capacity
	var storageCapacityTB float64
	if c.scanPath != "" {
		totalSize, err := dcp.CalculateDirectorySize(c.scanPath)
		if err == nil {
			storageCapacityTB = float64(totalSize) / (1024 * 1024 * 1024 * 1024)
		}
	}

	// Get package count
	var packageCount int
	err := c.db.QueryRow("SELECT COUNT(*) FROM dcp_packages").Scan(&packageCount)
	if err != nil {
		packageCount = 0
	}

	// Detect public IP
	publicIP := dcp.GetPublicIP()
	apiURL := ""
	if publicIP != "" {
		apiURL = fmt.Sprintf("http://%s:10858", publicIP)
	}

	heartbeat := NewMessage(MessageTypeHeartbeat, map[string]interface{}{
		"server_id":           c.serverID.String(),
		"server_name":         c.serverName,
		"mac_address":         c.macAddress,
		"software_version":    c.softwareVersion,
		"storage_capacity_tb": storageCapacityTB,
		"package_count":       packageCount,
		"public_ip":           publicIP,
		"api_url":             apiURL,
	})

	data, err := heartbeat.ToJSON()
	if err != nil {
		log.Printf("[WS Client] Failed to marshal heartbeat: %v", err)
		return
	}

	select {
	case c.send <- data:
	default:
		log.Printf("[WS Client] Failed to send heartbeat (buffer full)")
	}
}

// IsConnected returns whether the client is currently connected
func (c *ClientConnector) IsConnected() bool {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.connected
}

// SendMessage sends a message to the server
func (c *ClientConnector) SendMessage(msg *Message) error {
	data, err := msg.ToJSON()
	if err != nil {
		return err
	}

	c.connMu.RLock()
	connected := c.connected
	c.connMu.RUnlock()

	if !connected {
		return fmt.Errorf("not connected to server")
	}

	select {
	case c.send <- data:
		return nil
	default:
		return fmt.Errorf("send buffer full")
	}
}
