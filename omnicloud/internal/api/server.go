package api

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/omnicloud/omnicloud/internal/db"
)

// TriggerScanFunc is called to start a full library scan on this server (e.g. from Rescan in UI)
type TriggerScanFunc func()

// Server represents the HTTP API server
type Server struct {
	router          *mux.Router
	database        *db.DB
	db              *sql.DB  // Direct DB connection for torrent handlers
	port            int
	server          *http.Server
	registrationKey string
	selfServerID    *uuid.UUID   // when set, restart for this ID triggers local process restart
	triggerScan     TriggerScanFunc // when set, POST /scan/trigger runs a full scan
}

// NewServer creates a new API server. selfServerID is this process's server row ID; when restart is requested for it, the process will restart itself.
// triggerScan is optional; when set, allows HTTP-triggered full library scan (used by Rescan in UI).
func NewServer(database *db.DB, port int, registrationKey string, selfServerID *uuid.UUID, triggerScan TriggerScanFunc) *Server {
	s := &Server{
		router:          mux.NewRouter(),
		database:        database,
		db:              database.DB,
		port:            port,
		registrationKey: registrationKey,
		selfServerID:    selfServerID,
		triggerScan:     triggerScan,
	}

	s.setupRoutes()
	return s
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Handle CORS preflight for all paths first (so OPTIONS always gets CORS headers)
	s.router.Methods("OPTIONS").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Server-ID, X-MAC-Address")
		w.WriteHeader(http.StatusOK)
	})

	// API v1 routes
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// Apply middleware
	api.Use(s.loggingMiddleware)
	api.Use(s.corsMiddleware)

	// Release repository - serve release packages (must be before auth middleware)
	releasesDir := "/home/appbox/DCPCLOUDAPP/releases"
	s.router.PathPrefix("/releases/").Handler(
		http.StripPrefix("/releases/",
			http.FileServer(http.Dir(releasesDir))),
	)

	// Health check
	api.HandleFunc("/health", s.handleHealth).Methods("GET")

	// Scan (trigger and status) - used by this server and by main server when proxying to sites
	api.HandleFunc("/scan/trigger", s.handleScanTrigger).Methods("POST")
	api.HandleFunc("/scan/status", s.handleScanStatus).Methods("GET")

	// Server routes
	api.HandleFunc("/servers", s.handleListServers).Methods("GET")
	api.HandleFunc("/servers/register", s.handleRegisterServer).Methods("POST") // No auth required for initial registration
	api.HandleFunc("/servers/{id}", s.handleUpdateServer).Methods("PUT")         // Update server config (auth/deauth)
	api.HandleFunc("/servers/{id}", s.handleDeleteServer).Methods("DELETE")      // Delete server
	
	// Protected server routes (require authorization)
	apiAuth := api.PathPrefix("/servers/{id}").Subrouter()
	apiAuth.Use(s.authorizationMiddleware)
	apiAuth.HandleFunc("/heartbeat", s.handleHeartbeat).Methods("POST")
	apiAuth.HandleFunc("/inventory", s.handleUpdateInventory).Methods("POST")
	apiAuth.HandleFunc("/dcps", s.handleGetServerDCPs).Methods("GET")
	apiAuth.HandleFunc("/pending-action", s.handlePendingAction).Methods("GET")
	apiAuth.HandleFunc("/action-done", s.handleActionDone).Methods("POST")
	apiAuth.HandleFunc("/torrent-status", s.handleTorrentStatus).Methods("POST")
	apiAuth.HandleFunc("/dcp-metadata", s.handleDCPMetadata).Methods("POST")

	// DCP routes
	api.HandleFunc("/dcps", s.handleListDCPs).Methods("GET")
	api.HandleFunc("/dcps/{uuid}", s.handleGetDCP).Methods("GET")

	// Torrent routes
	api.HandleFunc("/torrents", s.handleListTorrents).Methods("GET")
	api.HandleFunc("/torrents", s.handleRegisterTorrent).Methods("POST")
	api.HandleFunc("/torrents/{info_hash}", s.handleGetTorrent).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/file", s.handleDownloadTorrentFile).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/seeders", s.handleListSeeders).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/seeders", s.handleRegisterSeeder).Methods("POST")
	
	// Torrent queue routes
	api.HandleFunc("/torrent-queue", s.handleListTorrentQueue).Methods("GET")
	api.HandleFunc("/torrent-queue/{id}", s.handleUpdateQueuePosition).Methods("PUT")
	api.HandleFunc("/torrent-queue/{id}/retry", s.handleRetryQueueItem).Methods("POST")
	api.HandleFunc("/torrent-queue/clear-completed", s.handleClearCompletedQueue).Methods("POST")

	// Transfer routes
	api.HandleFunc("/transfers", s.handleListTransfers).Methods("GET")
	api.HandleFunc("/transfers", s.handleCreateTransfer).Methods("POST")
	api.HandleFunc("/transfers/{id}", s.handleGetTransfer).Methods("GET")
	api.HandleFunc("/transfers/{id}", s.handleUpdateTransfer).Methods("PUT")
	api.HandleFunc("/transfers/{id}", s.handleDeleteTransfer).Methods("DELETE")

	// Software version routes
	api.HandleFunc("/versions", s.handleListVersions).Methods("GET")
	api.HandleFunc("/versions", s.handleRegisterVersion).Methods("POST")
	api.HandleFunc("/versions/latest", s.handleGetLatestVersion).Methods("GET")
	api.HandleFunc("/servers/{id}/upgrade", s.handleTriggerUpgrade).Methods("POST")
	api.HandleFunc("/servers/{id}/restart", s.handleRestartServer).Methods("POST")
	api.HandleFunc("/servers/{id}/rescan", s.handleRescanServer).Methods("POST")
	api.HandleFunc("/servers/{id}/scan-status", s.handleServerScanStatus).Methods("GET")

	// Serve static files for web UI (must be last to not conflict with API routes)
	webDir := filepath.Join(filepath.Dir(filepath.Dir(os.Args[0])), "web")
	s.router.PathPrefix("/").Handler(http.FileServer(http.Dir(webDir)))

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
