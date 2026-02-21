package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// AuthStatusResponse represents the authorization status for a client
type AuthStatusResponse struct {
	Authorized bool      `json:"authorized"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expires_at"`
	Message    string    `json:"message"`
	LastChecked time.Time `json:"last_checked"`
}

// handleAuthStatus checks and returns the authorization status of a server
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverIDStr := vars["id"]

	serverID, err := uuid.Parse(serverIDStr)
	if err != nil {
		http.Error(w, "Invalid server ID", http.StatusBadRequest)
		return
	}

	// Get server info
	query := `
		SELECT name, is_authorized, mac_address
		FROM servers
		WHERE id = $1
	`

	var serverName, macAddress string
	var isAuthorized bool
	err = s.db.QueryRow(query, serverID).Scan(&serverName, &isAuthorized, &macAddress)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Server not found", http.StatusNotFound)
			return
		}
		log.Printf("Database error: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Generate authorization token (unique to this server)
	token := generateAuthToken(serverID.String(), macAddress, time.Now())

	expiresAt := time.Now().Add(5 * time.Minute)
	status := AuthStatusResponse{
		Authorized:  isAuthorized,
		Token:       token,
		ExpiresAt:   expiresAt,
		LastChecked: time.Now(),
	}

	if isAuthorized {
		status.Message = "Client is authorized to run"
	} else {
		status.Message = "Client authorization is pending - operations are halted"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)

	// Log authorization check
	if isAuthorized {
		log.Printf("[Auth] ✅ Authorization confirmed for %s (%s)", serverName, serverID)
	} else {
		log.Printf("[Auth] ⚠️ Authorization denied for %s (%s) - client must halt operations", serverName, serverID)
	}
}

// generateAuthToken creates a unique authorization token
// This should be stored/transmitted securely (HTTPS only)
func generateAuthToken(serverID, macAddress string, issuedAt time.Time) string {
	// Create a token combining server ID, MAC, and timestamp
	data := fmt.Sprintf("%s:%s:%d", serverID, macAddress, issuedAt.Unix())
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// ValidateAuthToken validates an authorization token from a client
func (s *Server) validateAuthToken(serverID uuid.UUID, macAddress, token string) bool {
	// For now, simply verify the server is authorized
	// In production, you could validate the token signature

	query := `SELECT is_authorized FROM servers WHERE id = $1 AND mac_address = $2`
	var isAuthorized bool
	err := s.db.QueryRow(query, serverID, macAddress).Scan(&isAuthorized)
	if err != nil {
		return false
	}

	return isAuthorized
}

// requireAuthorized is a middleware that checks if a client is authorized
// Only applies to clients (not main server)
func (s *Server) requireAuthorized(serverID uuid.UUID) (bool, error) {
	query := `SELECT is_authorized FROM servers WHERE id = $1`
	var isAuthorized bool
	err := s.db.QueryRow(query, serverID).Scan(&isAuthorized)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Errorf("server not found")
		}
		return false, err
	}

	return isAuthorized, nil
}
