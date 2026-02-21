package websocket

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for WebSocket connections
		// In production, you might want to restrict this
		return true
	},
}

// Handler handles WebSocket connection requests
type Handler struct {
	hub *Hub
	db  *sql.DB
}

// NewHandler creates a new WebSocket handler
func NewHandler(hub *Hub, db *sql.DB) *Handler {
	return &Handler{
		hub: hub,
		db:  db,
	}
}

// ServeHTTP handles WebSocket upgrade requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Extract authentication parameters
	serverIDStr := r.URL.Query().Get("server_id")
	macAddress := r.URL.Query().Get("mac_address")
	registrationKey := r.URL.Query().Get("registration_key")

	if serverIDStr == "" || macAddress == "" {
		log.Printf("[WS Handler] Missing required parameters")
		http.Error(w, "Missing required parameters", http.StatusBadRequest)
		return
	}

	serverID, err := uuid.Parse(serverIDStr)
	if err != nil {
		log.Printf("[WS Handler] Invalid server ID: %v", err)
		http.Error(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	// Authenticate the client
	server, err := h.authenticateClient(serverID, macAddress, registrationKey)
	if err != nil {
		log.Printf("[WS Handler] Authentication failed for %s: %v", serverIDStr, err)
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS Handler] Failed to upgrade connection: %v", err)
		return
	}

	// Create client
	client := &Client{
		ID:           uuid.New(),
		ServerID:     serverID,
		ServerName:   server.Name,
		MACAddress:   macAddress,
		Conn:         conn,
		Send:         make(chan []byte, 256),
		Hub:          h.hub,
		LastSeen:     time.Now(),
		ConnectedAt:  time.Now(),
		IsAuthorized: server.IsAuthorized,
	}

	// Register client
	h.hub.register <- client

	// Start pumps
	go client.writePump()
	go client.readPump()

	log.Printf("[WS Handler] Client connected: %s (%s) from %s",
		server.Name, serverID, r.RemoteAddr)
}

// authenticateClient verifies the client's credentials
func (h *Handler) authenticateClient(serverID uuid.UUID, macAddress, registrationKey string) (*ServerInfo, error) {
	var server ServerInfo
	var storedMAC, registrationKeyHash string

	query := `
		SELECT name, location, mac_address, registration_key_hash, is_authorized
		FROM servers
		WHERE id = $1
	`

	err := h.db.QueryRow(query, serverID).Scan(
		&server.Name,
		&server.Location,
		&storedMAC,
		&registrationKeyHash,
		&server.IsAuthorized,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrServerNotFound
		}
		return nil, err
	}

	// Verify MAC address
	if storedMAC != macAddress {
		return nil, ErrInvalidMAC
	}

	// Verify registration key (if provided)
	if registrationKey != "" && registrationKeyHash != "" {
		if !verifyRegistrationKey(registrationKey, registrationKeyHash) {
			return nil, ErrInvalidKey
		}
	}

	server.ID = serverID
	server.MACAddress = macAddress

	return &server, nil
}

// ServerInfo contains basic server information
type ServerInfo struct {
	ID           uuid.UUID
	Name         string
	Location     string
	MACAddress   string
	IsAuthorized bool
}

// Error types
var (
	ErrServerNotFound = &AuthError{Message: "Server not found"}
	ErrInvalidMAC     = &AuthError{Message: "Invalid MAC address"}
	ErrInvalidKey     = &AuthError{Message: "Invalid registration key"}
)

// AuthError represents an authentication error
type AuthError struct {
	Message string
}

func (e *AuthError) Error() string {
	return e.Message
}

// verifyRegistrationKey checks if a key matches the stored hash
func verifyRegistrationKey(key, hash string) bool {
	// This should match the hashing logic in your handlers.go
	// Using SHA256
	return hashRegistrationKey(key) == hash
}

// hashRegistrationKey creates a SHA256 hash of the registration key
func hashRegistrationKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}
