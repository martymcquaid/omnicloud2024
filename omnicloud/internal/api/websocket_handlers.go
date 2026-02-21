package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	ws "github.com/omnicloud/omnicloud/internal/websocket"
)

// handleWebSocket upgrades HTTP connection to WebSocket for client communication
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		log.Printf("[WS API] WebSocket hub not initialized")
		http.Error(w, "WebSocket not available", http.StatusServiceUnavailable)
		return
	}

	// Create WebSocket handler
	handler := ws.NewHandler(s.wsHub, s.db)
	handler.ServeHTTP(w, r)
}

// handleSendServerCommand sends a command to a connected client via WebSocket
func (s *Server) handleSendServerCommand(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		respondError(w, http.StatusServiceUnavailable, "WebSocket not available", "")
		return
	}

	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var request struct {
		Command string      `json:"command"`
		Payload interface{} `json:"payload,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	// Check if client is connected
	if !s.wsHub.IsClientConnected(serverID) {
		respondError(w, http.StatusServiceUnavailable, "Client not connected", "")
		return
	}

	// Parse command type
	var cmdType ws.CommandType
	switch request.Command {
	case "restart":
		cmdType = ws.CommandRestart
	case "upgrade":
		cmdType = ws.CommandUpgrade
	case "rescan":
		cmdType = ws.CommandRescan
	case "status_update":
		cmdType = ws.CommandStatusUpdate
	default:
		respondError(w, http.StatusBadRequest, "Unknown command", request.Command)
		return
	}

	// Send command via WebSocket
	if err := s.wsHub.SendCommandToClient(serverID, cmdType, request.Payload); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to send command", err.Error())
		return
	}

	log.Printf("[WS API] Command '%s' sent to server %s", request.Command, serverID)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":   "Command sent successfully",
		"command":   request.Command,
		"server_id": serverID,
	})
}

// handleListWebSocketClients returns a list of all connected WebSocket clients
func (s *Server) handleListWebSocketClients(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		respondError(w, http.StatusServiceUnavailable, "WebSocket not available", "")
		return
	}

	connectedIDs := s.wsHub.GetConnectedClients()
	clients := []map[string]interface{}{}

	for _, id := range connectedIDs {
		if info := s.wsHub.GetClientInfo(id); info != nil {
			clients = append(clients, info)
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"clients": clients,
		"count":   len(clients),
	})
}

// handleGetWebSocketClientStatus returns the connection status of a specific client
func (s *Server) handleGetWebSocketClientStatus(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		respondError(w, http.StatusServiceUnavailable, "WebSocket not available", "")
		return
	}

	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	connected := s.wsHub.IsClientConnected(serverID)
	info := s.wsHub.GetClientInfo(serverID)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"server_id": serverID,
		"connected": connected,
		"info":      info,
	})
}

// handleDeleteContent sends a delete_content command to a client via WebSocket and waits for the result.
// Also handles cancelling any active transfers and cleaning up main-server inventory on success.
func (s *Server) handleDeleteContent(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		respondError(w, http.StatusServiceUnavailable, "WebSocket not available", "")
		return
	}

	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var req struct {
		PackageID  string `json:"package_id"`
		TargetPath string `json:"target_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if req.PackageID == "" {
		respondError(w, http.StatusBadRequest, "package_id is required", "")
		return
	}

	// Check if client is connected via WebSocket
	if !s.wsHub.IsClientConnected(serverID) {
		respondError(w, http.StatusServiceUnavailable, "Client not connected via WebSocket", "")
		return
	}

	// Look up package name and info hash
	var packageName string
	var infoHash sql.NullString
	err = s.db.QueryRow(`
		SELECT dp.package_name, dt.info_hash
		FROM dcp_packages dp
		LEFT JOIN dcp_torrents dt ON dt.package_id = dp.id
		WHERE dp.id = $1
	`, req.PackageID).Scan(&packageName, &infoHash)
	if err != nil {
		respondError(w, http.StatusNotFound, "Package not found", "")
		return
	}

	ih := ""
	if infoHash.Valid {
		ih = infoHash.String
	}

	// Cancel any active transfer for this package on this server
	var cancelledTransferID string
	_ = s.db.QueryRow(`
		SELECT t.id FROM transfers t
		JOIN dcp_torrents dt ON dt.id = t.torrent_id
		WHERE dt.package_id = $1 AND t.destination_server_id = $2
		AND t.status IN ('downloading', 'paused', 'checking', 'queued', 'error', 'failed')
		ORDER BY t.created_at DESC LIMIT 1
	`, req.PackageID, serverID).Scan(&cancelledTransferID)

	if cancelledTransferID != "" {
		_, _ = s.db.Exec(`
			UPDATE transfers SET status = 'cancelled', delete_data = true,
			pending_command = 'cancel', command_acknowledged = false, updated_at = $1
			WHERE id = $2
		`, time.Now(), cancelledTransferID)
		log.Printf("[delete-content] Cancelled transfer %s for package %s on server %s", cancelledTransferID, packageName, serverID)
	}

	// Send delete_content command via WebSocket and wait for response
	payload := map[string]interface{}{
		"package_id":   req.PackageID,
		"package_name": packageName,
		"info_hash":    ih,
		"target_path":  req.TargetPath,
	}

	log.Printf("[delete-content] Sending delete command to server %s for package %s", serverID, packageName)

	resp, err := s.wsHub.SendCommandAndWait(serverID, ws.CommandDeleteContent, payload, 30*time.Second)
	if err != nil {
		log.Printf("[delete-content] Error: %v", err)
		respondError(w, http.StatusGatewayTimeout, "Timeout or error waiting for client response", err.Error())
		return
	}

	// On successful deletion, clean up main server inventory
	if resp.Success && req.TargetPath == "" {
		_, err = s.db.Exec("DELETE FROM server_dcp_inventory WHERE server_id = $1 AND package_id = $2", serverID, req.PackageID)
		if err != nil {
			log.Printf("[delete-content] Warning: failed to remove inventory for server=%s package=%s: %v", serverID, req.PackageID, err)
		} else {
			log.Printf("[delete-content] Removed inventory entry for server=%s package=%s", serverID, req.PackageID)
		}

		// Also clean up ingestion status
		_, _ = s.db.Exec("DELETE FROM dcp_ingestion_status WHERE server_id = $1 AND package_id = $2", serverID, req.PackageID)

		// Clean up torrent seeders
		if ih != "" {
			_, _ = s.db.Exec("DELETE FROM torrent_seeders WHERE server_id = $1 AND torrent_id IN (SELECT id FROM dcp_torrents WHERE info_hash = $2)", serverID, ih)
		}
	}

	log.Printf("[delete-content] Result from server %s: success=%v message=%s", serverID, resp.Success, resp.Message)

	s.logActivity(r, "content.delete", "content", "package", req.PackageID, "", "", "success")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": resp.Success,
		"message": resp.Message,
		"error":   resp.Error,
	})
}
