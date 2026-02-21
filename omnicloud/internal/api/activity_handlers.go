package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// handleGetAllServerActivities returns the latest activity reports for all connected servers
func (s *Server) handleGetAllServerActivities(w http.ResponseWriter, r *http.Request) {
	if s.wsHub == nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"activities": []interface{}{},
		})
		return
	}

	allActivities := s.wsHub.ActivityStore.GetAll()

	result := make([]map[string]interface{}, 0, len(allActivities))
	for _, activity := range allActivities {
		result = append(result, map[string]interface{}{
			"server_id":   activity.ServerID,
			"server_name": activity.ServerName,
			"updated_at":  activity.UpdatedAt,
			"activities":  activity.Activities,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"servers": result,
		"count":   len(result),
	})
}

// handleGetServerActivity returns the latest activity report for a specific server
func (s *Server) handleGetServerActivity(w http.ResponseWriter, r *http.Request) {
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

	activity := s.wsHub.ActivityStore.Get(serverID)
	if activity == nil {
		// No activity data yet - return empty
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"server_id":  serverID,
			"activities": []interface{}{},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"server_id":   activity.ServerID,
		"server_name": activity.ServerName,
		"updated_at":  activity.UpdatedAt,
		"activities":  activity.Activities,
	})
}
