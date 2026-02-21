package api

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
)

// logActivity records a user action to the activity_logs table asynchronously.
// It extracts the user from the Bearer token (best-effort) and the client IP.
func (s *Server) logActivity(r *http.Request, action, category, resourceType, resourceID, resourceName, details, status string) {
	var userID *uuid.UUID
	username := "system"

	token := extractBearerToken(r)
	if token != "" {
		session, err := s.database.GetSession(token)
		if err == nil && session != nil {
			user, err := s.database.GetUserByID(session.UserID)
			if err == nil && user != nil {
				userID = &user.ID
				username = user.Username
			}
		}
	}

	// Extract client IP
	ip := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ip = strings.Split(forwarded, ",")[0]
		ip = strings.TrimSpace(ip)
	}
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}

	go func() {
		entry := &db.ActivityLog{
			UserID:       userID,
			Username:     username,
			Action:       action,
			Category:     category,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			ResourceName: resourceName,
			Details:      details,
			IPAddress:    ip,
			Status:       status,
		}
		if err := s.database.CreateActivityLog(entry); err != nil {
			log.Printf("[activity-log] Error logging %s: %v", action, err)
		}
	}()
}

// logActivityWithUser is like logActivity but uses a known user directly (for login handler)
func (s *Server) logActivityWithUser(r *http.Request, userID *uuid.UUID, username, action, category, resourceType, resourceID, resourceName, details, status string) {
	ip := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ip = strings.Split(forwarded, ",")[0]
		ip = strings.TrimSpace(ip)
	}
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}

	go func() {
		entry := &db.ActivityLog{
			UserID:       userID,
			Username:     username,
			Action:       action,
			Category:     category,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			ResourceName: resourceName,
			Details:      details,
			IPAddress:    ip,
			Status:       status,
		}
		if err := s.database.CreateActivityLog(entry); err != nil {
			log.Printf("[activity-log] Error logging %s: %v", action, err)
		}
	}()
}

// --- API Handlers ---

type activityLogResponse struct {
	ID           string `json:"id"`
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	Action       string `json:"action"`
	Category     string `json:"category"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	ResourceName string `json:"resource_name"`
	Details      string `json:"details"`
	IPAddress    string `json:"ip_address"`
	Status       string `json:"status"`
	CreatedAt    string `json:"created_at"`
}

type activityLogListResponse struct {
	Logs   []activityLogResponse `json:"logs"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

type activityLogStatsResponse struct {
	Total      int            `json:"total"`
	TodayCount int            `json:"today_count"`
	ByCategory map[string]int `json:"by_category"`
}

func (s *Server) handleListActivityLogs(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}

	q := r.URL.Query()
	filter := db.ActivityLogFilter{
		Category: q.Get("category"),
		Action:   q.Get("action"),
		Search:   q.Get("search"),
	}

	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Offset = n
		}
	}
	if v := q.Get("start"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			filter.StartDate = &t
		}
	}
	if v := q.Get("end"); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			endOfDay := t.Add(24*time.Hour - time.Second)
			filter.EndDate = &endOfDay
		}
	}

	logs, total, err := s.database.ListActivityLogs(filter)
	if err != nil {
		log.Printf("[activity-log] Error listing: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to list activity logs", "")
		return
	}

	resp := activityLogListResponse{
		Logs:   make([]activityLogResponse, 0, len(logs)),
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	for _, l := range logs {
		uid := ""
		if l.UserID != nil {
			uid = l.UserID.String()
		}
		resp.Logs = append(resp.Logs, activityLogResponse{
			ID:           l.ID.String(),
			UserID:       uid,
			Username:     l.Username,
			Action:       l.Action,
			Category:     l.Category,
			ResourceType: l.ResourceType,
			ResourceID:   l.ResourceID,
			ResourceName: l.ResourceName,
			Details:      l.Details,
			IPAddress:    l.IPAddress,
			Status:       l.Status,
			CreatedAt:    l.CreatedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleGetActivityLogStats(w http.ResponseWriter, r *http.Request) {
	if s.requireAdmin(w, r) == nil {
		return
	}

	total, todayCount, byCategory, err := s.database.GetActivityLogStats()
	if err != nil {
		log.Printf("[activity-log] Error getting stats: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to get activity stats", "")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(activityLogStatsResponse{
		Total:      total,
		TodayCount: todayCount,
		ByCategory: byCategory,
	})
}
