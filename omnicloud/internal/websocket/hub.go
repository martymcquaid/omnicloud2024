package websocket

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Client represents a connected client
type Client struct {
	ID             uuid.UUID
	ServerID       uuid.UUID
	ServerName     string
	MACAddress     string
	Conn           *websocket.Conn
	Send           chan []byte
	Hub            *Hub
	LastSeen       time.Time
	ConnectedAt    time.Time
	IsAuthorized   bool
	mu             sync.RWMutex
}

// Hub manages all WebSocket connections
type Hub struct {
	// Registered clients by server ID
	clients    map[uuid.UUID]*Client
	clientsMu  sync.RWMutex

	// Register requests from clients
	register   chan *Client

	// Unregister requests from clients
	unregister chan *Client

	// Broadcast message to all clients
	broadcast  chan []byte

	// Send message to specific client
	unicast    chan *unicastMessage

	// Database connection for updating last_seen
	db         *sql.DB

	// Activity store for live server activities
	ActivityStore *ActivityStore

	// Response channels for synchronous command-response flows
	responseChs   map[string]chan *ResponseMessage
	responseChsMu sync.Mutex
}

type unicastMessage struct {
	serverID uuid.UUID
	data     []byte
}

// NewHub creates a new WebSocket hub
func NewHub(db *sql.DB) *Hub {
	return &Hub{
		clients:       make(map[uuid.UUID]*Client),
		register:      make(chan *Client),
		unregister:    make(chan *Client),
		broadcast:     make(chan []byte, 256),
		unicast:       make(chan *unicastMessage, 256),
		db:            db,
		ActivityStore: NewActivityStore(),
		responseChs:   make(map[string]chan *ResponseMessage),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	// Periodic cleanup and status update
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastMessage(message)

		case msg := <-h.unicast:
			h.unicastMessage(msg.serverID, msg.data)

		case <-ticker.C:
			h.cleanupStaleConnections()
			h.updateServerStatus()
		}
	}
}

// registerClient adds a client to the hub
func (h *Hub) registerClient(client *Client) {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()

	// Close existing connection if any
	if existing, ok := h.clients[client.ServerID]; ok {
		log.Printf("[WS Hub] Replacing existing connection for server %s (%s)",
			client.ServerName, client.ServerID)
		close(existing.Send)
	}

	h.clients[client.ServerID] = client

	log.Printf("[WS Hub] Client registered: %s (%s) - Total clients: %d",
		client.ServerName, client.ServerID, len(h.clients))

	// Update server status to online
	h.updateServerOnlineStatus(client.ServerID, true)
}

// unregisterClient removes a client from the hub
func (h *Hub) unregisterClient(client *Client) {
	h.clientsMu.Lock()
	defer h.clientsMu.Unlock()

	if _, ok := h.clients[client.ServerID]; ok {
		delete(h.clients, client.ServerID)
		close(client.Send)

		log.Printf("[WS Hub] Client unregistered: %s (%s) - Total clients: %d",
			client.ServerName, client.ServerID, len(h.clients))

		// Update server status to offline
		h.updateServerOnlineStatus(client.ServerID, false)
	}
}

// broadcastMessage sends a message to all connected clients
func (h *Hub) broadcastMessage(message []byte) {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	for _, client := range h.clients {
		select {
		case client.Send <- message:
		default:
			// Client send buffer is full, disconnect
			log.Printf("[WS Hub] Client send buffer full, disconnecting: %s", client.ServerID)
			go func(c *Client) {
				h.unregister <- c
			}(client)
		}
	}
}

// unicastMessage sends a message to a specific client
func (h *Hub) unicastMessage(serverID uuid.UUID, message []byte) {
	h.clientsMu.RLock()
	client, ok := h.clients[serverID]
	h.clientsMu.RUnlock()

	if !ok {
		log.Printf("[WS Hub] Client not connected: %s", serverID)
		return
	}

	select {
	case client.Send <- message:
	default:
		log.Printf("[WS Hub] Failed to send to client %s (buffer full)", serverID)
	}
}

// SendToClient sends a message to a specific client (public method)
func (h *Hub) SendToClient(serverID uuid.UUID, message []byte) {
	h.unicast <- &unicastMessage{
		serverID: serverID,
		data:     message,
	}
}

// SendCommandToClient sends a command to a specific client
func (h *Hub) SendCommandToClient(serverID uuid.UUID, command CommandType, payload interface{}) error {
	cmd := NewCommandMessage(command, payload)
	data, err := cmd.ToJSON()
	if err != nil {
		return err
	}

	h.SendToClient(serverID, data)
	return nil
}

// BroadcastMessage sends a message to all connected clients (public method)
func (h *Hub) BroadcastMessage(message []byte) {
	h.broadcast <- message
}

// DeliverResponse routes a response message to any waiting channel
func (h *Hub) DeliverResponse(resp *ResponseMessage) {
	h.responseChsMu.Lock()
	ch, ok := h.responseChs[resp.MessageID]
	if ok {
		delete(h.responseChs, resp.MessageID)
	}
	h.responseChsMu.Unlock()

	if ok {
		select {
		case ch <- resp:
		default:
		}
	}
}

// SendCommandAndWait sends a command to a client and waits for the response
func (h *Hub) SendCommandAndWait(serverID uuid.UUID, command CommandType, payload interface{}, timeout time.Duration) (*ResponseMessage, error) {
	if !h.IsClientConnected(serverID) {
		return nil, fmt.Errorf("client not connected")
	}

	cmd := NewCommandMessage(command, payload)
	data, err := cmd.ToJSON()
	if err != nil {
		return nil, err
	}

	// Register response channel before sending
	ch := make(chan *ResponseMessage, 1)
	h.responseChsMu.Lock()
	h.responseChs[cmd.MessageID] = ch
	h.responseChsMu.Unlock()

	// Clean up on exit
	defer func() {
		h.responseChsMu.Lock()
		delete(h.responseChs, cmd.MessageID)
		h.responseChsMu.Unlock()
	}()

	// Send the command
	h.SendToClient(serverID, data)

	// Wait for response with timeout
	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for response from client")
	}
}

// IsClientConnected checks if a client is currently connected
func (h *Hub) IsClientConnected(serverID uuid.UUID) bool {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	_, ok := h.clients[serverID]
	return ok
}

// GetConnectedClients returns a list of all connected client IDs
func (h *Hub) GetConnectedClients() []uuid.UUID {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	ids := make([]uuid.UUID, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	return ids
}

// GetClientInfo returns information about a connected client
func (h *Hub) GetClientInfo(serverID uuid.UUID) map[string]interface{} {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	client, ok := h.clients[serverID]
	if !ok {
		return nil
	}

	client.mu.RLock()
	defer client.mu.RUnlock()

	return map[string]interface{}{
		"server_id":     client.ServerID,
		"server_name":   client.ServerName,
		"mac_address":   client.MACAddress,
		"connected_at":  client.ConnectedAt,
		"last_seen":     client.LastSeen,
		"is_authorized": client.IsAuthorized,
	}
}

// cleanupStaleConnections removes clients that haven't sent a heartbeat
func (h *Hub) cleanupStaleConnections() {
	h.clientsMu.RLock()
	staleClients := []*Client{}
	timeout := 2 * time.Minute

	for _, client := range h.clients {
		client.mu.RLock()
		if time.Since(client.LastSeen) > timeout {
			staleClients = append(staleClients, client)
		}
		client.mu.RUnlock()
	}
	h.clientsMu.RUnlock()

	// Disconnect stale clients
	for _, client := range staleClients {
		log.Printf("[WS Hub] Disconnecting stale client: %s (last seen %v ago)",
			client.ServerID, time.Since(client.LastSeen))
		h.unregister <- client
	}
}

// updateServerStatus updates last_seen for all connected clients
func (h *Hub) updateServerStatus() {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()

	now := time.Now()
	for _, client := range h.clients {
		client.mu.RLock()
		serverID := client.ServerID
		client.mu.RUnlock()

		_, err := h.db.Exec(`
			UPDATE servers
			SET last_seen = $1, updated_at = $2
			WHERE id = $3
		`, now, now, serverID)

		if err != nil {
			log.Printf("[WS Hub] Failed to update server status for %s: %v", serverID, err)
		}
	}
}

// updateServerOnlineStatus updates the server's online status
func (h *Hub) updateServerOnlineStatus(serverID uuid.UUID, online bool) {
	now := time.Now()

	if online {
		_, err := h.db.Exec(`
			UPDATE servers
			SET last_seen = $1, updated_at = $2
			WHERE id = $3
		`, now, now, serverID)

		if err != nil {
			log.Printf("[WS Hub] Failed to update server online status for %s: %v", serverID, err)
		}
	} else {
		// When client disconnects, just update the timestamp
		// The frontend will consider servers offline based on last_seen age
		_, err := h.db.Exec(`
			UPDATE servers
			SET updated_at = $1
			WHERE id = $2
		`, now, serverID)

		if err != nil {
			log.Printf("[WS Hub] Failed to update server offline status for %s: %v", serverID, err)
		}
	}
}

// writePump pumps messages from the hub to the websocket connection
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// Hub closed the channel
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Printf("[WS Client] Write error for %s: %v", c.ServerID, err)
				return
			}

		case <-ticker.C:
			// Send ping
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				log.Printf("[WS Client] Ping error for %s: %v", c.ServerID, err)
				return
			}
		}
	}
}

// readPump pumps messages from the websocket connection to the hub
func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		c.mu.Lock()
		c.LastSeen = time.Now()
		c.mu.Unlock()
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[WS Client] Read error for %s: %v", c.ServerID, err)
			}
			break
		}

		c.mu.Lock()
		c.LastSeen = time.Now()
		c.mu.Unlock()

		// Handle message
		c.handleMessage(message)
	}
}

// handleMessage processes incoming messages from client
func (c *Client) handleMessage(data []byte) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		log.Printf("[WS Client] Failed to parse message from %s: %v", c.ServerID, err)
		return
	}

	switch msg.Type {
	case MessageTypePing:
		// Respond with pong
		pong := NewMessage(MessageTypePong, nil)
		if data, err := pong.ToJSON(); err == nil {
			c.Send <- data
		}

	case MessageTypeHeartbeat:
		// Update server information
		c.handleHeartbeat(msg.Payload)

	case MessageTypeResponse:
		// Parse full response and deliver to any waiting channel
		var resp ResponseMessage
		if err := json.Unmarshal(data, &resp); err == nil {
			c.Hub.DeliverResponse(&resp)
		}
		log.Printf("[WS Client] Response from %s: success=%v message=%s", c.ServerID, msg.Payload["success"], msg.Payload["message"])

	case MessageTypeStatus:
		// Handle status update
		log.Printf("[WS Client] Status update from %s: %+v", c.ServerID, msg.Payload)

	case MessageTypeActivity:
		// Handle activity report
		c.handleActivity(msg.Payload)

	default:
		log.Printf("[WS Client] Unknown message type from %s: %s", c.ServerID, msg.Type)
	}
}

// handleActivity processes activity report messages and stores them
func (c *Client) handleActivity(payload map[string]interface{}) {
	// Parse activities from payload
	activitiesRaw, ok := payload["activities"]
	if !ok {
		return
	}

	// Convert to JSON and back to get typed items
	jsonBytes, err := json.Marshal(activitiesRaw)
	if err != nil {
		log.Printf("[WS Client] Failed to marshal activity from %s: %v", c.ServerID, err)
		return
	}

	var items []ActivityItem
	if err := json.Unmarshal(jsonBytes, &items); err != nil {
		log.Printf("[WS Client] Failed to parse activity items from %s: %v", c.ServerID, err)
		return
	}

	// Store in activity store
	c.Hub.ActivityStore.Update(c.ServerID, c.ServerName, items)
}

// handleHeartbeat processes heartbeat messages
func (c *Client) handleHeartbeat(payload map[string]interface{}) {
	// Extract heartbeat data
	storageCapacity, _ := payload["storage_capacity_tb"].(float64)
	softwareVersion, _ := payload["software_version"].(string)
	packageCount, _ := payload["package_count"].(float64)
	publicIP, _ := payload["public_ip"].(string)
	apiURL, _ := payload["api_url"].(string)

	// Update server record in database
	now := time.Now()
	_, err := c.Hub.db.Exec(`
		UPDATE servers
		SET last_seen = $1,
		    updated_at = $2,
		    storage_capacity_tb = COALESCE(NULLIF($3, 0), storage_capacity_tb),
		    software_version = COALESCE(NULLIF($4, ''), software_version),
		    api_url = COALESCE(NULLIF($5, ''), api_url)
		WHERE id = $6
	`, now, now, storageCapacity, softwareVersion, apiURL, c.ServerID)

	if err != nil {
		log.Printf("[WS Client] Failed to update heartbeat for %s: %v", c.ServerID, err)
	} else {
		log.Printf("[WS Client] Heartbeat from %s: %.2fTB, %s, %d packages, IP: %s",
			c.ServerName, storageCapacity, softwareVersion, int(packageCount), publicIP)
	}
}
