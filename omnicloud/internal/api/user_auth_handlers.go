package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/omnicloud/omnicloud/internal/db"
)

// loginRequest is the JSON body for POST /auth/login
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse is returned on successful login
type loginResponse struct {
	Token        string   `json:"token"`
	Username     string   `json:"username"`
	Role         string   `json:"role"`
	AllowedPages []string `json:"allowed_pages"`
	ExpiresAt    string   `json:"expires_at"`
}

// sessionResponse is returned by GET /auth/session
type sessionResponse struct {
	Authenticated bool     `json:"authenticated"`
	Username      string   `json:"username,omitempty"`
	Role          string   `json:"role,omitempty"`
	AllowedPages  []string `json:"allowed_pages,omitempty"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
}

// getUserAllowedPages fetches the allowed pages for a user's role
func (s *Server) getUserAllowedPages(role string) []string {
	perm, err := s.database.GetRolePermissions(role)
	if err != nil || perm == nil {
		// Fallback: admin gets everything, others get dashboard only
		if role == "admin" {
			return []string{"dashboard", "dcps", "servers", "transfers", "torrents", "torrent-status", "tracker", "analytics", "settings"}
		}
		return []string{"dashboard"}
	}
	return parseAllowedPages(perm.AllowedPages)
}

// handleLogin authenticates a user and returns a session token
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request", "Expected JSON with username and password")
		return
	}

	if req.Username == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "Missing credentials", "Username and password are required")
		return
	}

	// Authenticate against database
	user, err := s.database.AuthenticateUser(req.Username, req.Password)
	if err != nil {
		log.Printf("[Auth] Database error during login: %v", err)
		respondError(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	if user == nil {
		log.Printf("[Auth] Failed login attempt for user: %s", req.Username)
		s.logActivityWithUser(r, nil, req.Username, "user.login_failed", "auth", "user", "", req.Username, "", "failure")
		respondError(w, http.StatusUnauthorized, "Invalid credentials", "Username or password is incorrect")
		return
	}

	// Generate secure random session token
	tokenBytes := make([]byte, 48)
	if _, err := rand.Read(tokenBytes); err != nil {
		log.Printf("[Auth] Failed to generate session token: %v", err)
		respondError(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	// Create session (expires in 7 days)
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	session := &db.UserSession{
		Token:     token,
		UserID:    user.ID,
		CreatedAt: time.Now(),
		ExpiresAt: expiresAt,
	}
	if err := s.database.CreateSession(session); err != nil {
		log.Printf("[Auth] Failed to create session: %v", err)
		respondError(w, http.StatusInternalServerError, "Internal error", "")
		return
	}

	// Clean up expired sessions periodically
	go s.database.DeleteExpiredSessions()

	log.Printf("[Auth] User '%s' logged in successfully", user.Username)
	s.logActivityWithUser(r, &user.ID, user.Username, "user.login", "auth", "user", user.ID.String(), user.Username, "", "success")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(loginResponse{
		Token:        token,
		Username:     user.Username,
		Role:         user.Role,
		AllowedPages: s.getUserAllowedPages(user.Role),
		ExpiresAt:    expiresAt.Format(time.RFC3339),
	})
}

// handleLogout invalidates the session token
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		respondError(w, http.StatusBadRequest, "Missing token", "Authorization header required")
		return
	}

	// Log activity before deleting session (so we can resolve the user)
	s.logActivity(r, "user.logout", "auth", "user", "", "", "", "success")

	if err := s.database.DeleteSession(token); err != nil {
		log.Printf("[Auth] Error deleting session: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Logged out successfully"})
}

// handleSessionCheck returns current session info
func (s *Server) handleSessionCheck(w http.ResponseWriter, r *http.Request) {
	token := extractBearerToken(r)
	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessionResponse{Authenticated: false})
		return
	}

	session, err := s.database.GetSession(token)
	if err != nil || session == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessionResponse{Authenticated: false})
		return
	}

	user, err := s.database.GetUserByID(session.UserID)
	if err != nil || user == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sessionResponse{Authenticated: false})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessionResponse{
		Authenticated: true,
		Username:      user.Username,
		Role:          user.Role,
		AllowedPages:  s.getUserAllowedPages(user.Role),
		ExpiresAt:     session.ExpiresAt.Format(time.RFC3339),
	})
}

// extractBearerToken pulls the token from "Authorization: Bearer <token>"
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
