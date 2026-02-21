package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/torrent"
)

// Server represents the HTTP API server
type Server struct {
	router          *mux.Router
	database        *db.DB
	db              *sql.DB // Direct DB connection for torrent handlers
	tracker         *torrent.Tracker
	port            int
	server          *http.Server
	registrationKey string
}

// NewServer creates a new API server
func NewServer(database *db.DB, port int, registrationKey string) *Server {
	s := &Server{
		router:          mux.NewRouter(),
		database:        database,
		db:              database.DB,
		port:            port,
		registrationKey: registrationKey,
	}

	s.setupRoutes()
	return s
}

func (s *Server) RegisterTracker(t *torrent.Tracker) {
	s.tracker = t
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Apply CORS globally so even 404 responses include CORS headers for browser clients.
	s.router.Use(s.corsMiddleware)

	// API v1 routes
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// Apply middleware
	api.Use(s.loggingMiddleware)
	api.Use(s.corsMiddleware)

	// Health check
	api.HandleFunc("/health", s.handleHealth).Methods("GET")

	// Server routes
	api.HandleFunc("/servers", s.handleListServers).Methods("GET")
	api.HandleFunc("/servers/register", s.handleRegisterServer).Methods("POST")
	api.HandleFunc("/servers/{id}/heartbeat", s.handleHeartbeat).Methods("POST")
	api.HandleFunc("/servers/{id}/inventory", s.handleUpdateInventory).Methods("POST")
	api.HandleFunc("/servers/{id}/torrent-status", s.handleTorrentStatus).Methods("POST")
	api.HandleFunc("/servers/{id}/dcps", s.handleGetServerDCPs).Methods("GET")
	api.HandleFunc("/servers/{id}/pending-transfers", s.handleGetPendingTransfers).Methods("GET")
	api.HandleFunc("/servers/{id}/rescan", s.handleRescanServer).Methods("POST")
	api.HandleFunc("/servers/{id}/scan-status", s.handleServerScanStatus).Methods("GET")
	api.HandleFunc("/servers/{id}/pending-action", s.handlePendingAction).Methods("GET")
	api.HandleFunc("/servers/{id}/action-done", s.handleActionDone).Methods("POST")

	// DCP routes
	api.HandleFunc("/dcps", s.handleListDCPs).Methods("GET")
	api.HandleFunc("/dcps/{uuid}", s.handleGetDCP).Methods("GET")

	// Torrent routes
	api.HandleFunc("/torrents", s.handleListTorrents).Methods("GET")
	api.HandleFunc("/torrents", s.handleRegisterTorrent).Methods("POST")
	api.HandleFunc("/torrents/{info_hash}", s.handleGetTorrent).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/file", s.handleDownloadTorrentFile).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/seeders", s.handleListSeeders).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/announce-attempts", s.handleListAnnounceAttempts).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/seeders", s.handleRegisterSeeder).Methods("POST")
	api.HandleFunc("/tracker/live", s.handleTrackerLive).Methods("GET")
	api.HandleFunc("/torrent-queue", s.handleListTorrentQueue).Methods("GET")
	api.HandleFunc("/torrent-queue/check", s.handleTorrentQueueCheck).Methods("GET")

	// Transfer routes
	api.HandleFunc("/transfers", s.handleListTransfers).Methods("GET")
	api.HandleFunc("/transfers", s.handleCreateTransfer).Methods("POST")
	api.HandleFunc("/transfers/{id}", s.handleGetTransfer).Methods("GET")
	api.HandleFunc("/transfers/{id}", s.handleUpdateTransfer).Methods("PUT")
	api.HandleFunc("/transfers/{id}", s.handleDeleteTransfer).Methods("DELETE")

	log.Println("API routes configured")
}

// Start starts the HTTP server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	log.Printf("Starting API server on %s", addr)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	log.Println("Shutting down API server...")
	return s.server.Shutdown(ctx)
}
