package api

import (
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// loggingMiddleware logs all HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a response writer that captures status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		
		next.ServeHTTP(wrapped, r)
		
		duration := time.Since(start)
		log.Printf("%s %s %d %v", r.Method, r.RequestURI, wrapped.statusCode, duration)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// corsMiddleware adds CORS headers
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Server-ID, X-MAC-Address")
		
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		
		next.ServeHTTP(w, r)
	})
}

// authorizationMiddleware checks if a server is authorized
// This middleware requires X-Server-ID or X-MAC-Address header
func (s *Server) authorizationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for server ID in header
		serverIDStr := r.Header.Get("X-Server-ID")
		macAddress := r.Header.Get("X-MAC-Address")
		
		// Try to get server ID from URL params (for inventory updates, etc)
		if serverIDStr == "" {
			vars := mux.Vars(r)
			serverIDStr = vars["id"]
		}
		
		// If we have neither, deny access
		if serverIDStr == "" && macAddress == "" {
			respondError(w, http.StatusUnauthorized, "Missing authentication", "X-Server-ID or X-MAC-Address header required")
			return
		}
		
		// Look up server by ID or MAC address
		var isAuthorized bool
		var serverID uuid.UUID
		var err error
		
		if serverIDStr != "" {
			serverID, err = uuid.Parse(serverIDStr)
			if err != nil {
				respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
				return
			}
			
			// Check authorization by server ID
			query := `SELECT is_authorized FROM servers WHERE id = $1`
			err = s.database.QueryRow(query, serverID).Scan(&isAuthorized)
		} else {
			// Check authorization by MAC address
			query := `SELECT id, is_authorized FROM servers WHERE mac_address = $1`
			err = s.database.QueryRow(query, macAddress).Scan(&serverID, &isAuthorized)
		}
		
		if err != nil {
			log.Printf("Authorization check failed: %v", err)
			respondError(w, http.StatusUnauthorized, "Server not found or unauthorized", "Please register with the main server first")
			return
		}
		
		if !isAuthorized {
			log.Printf("Unauthorized access attempt from server %s (MAC: %s)", serverID, macAddress)
			respondError(w, http.StatusForbidden, "Server not authorized", "Your server has not been authorized by the administrator")
			return
		}
		
		// Server is authorized, continue
		next.ServeHTTP(w, r)
	})
}
