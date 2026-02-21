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
	ws "github.com/omnicloud/omnicloud/internal/websocket"
)

// TriggerScanFunc is called to start a full library scan on this server (e.g. from Rescan in UI)
type TriggerScanFunc func()

// Server represents the HTTP API server
type Server struct {
	router          *mux.Router
	database        *db.DB
	db              *sql.DB // Direct DB connection for torrent handlers
	port            int
	trackerPort     int          // Tracker port (main server); 0 = do not rewrite announce URL when serving .torrent
	trackerHandler  http.Handler // optional; when set, /announce is served on the same port (avoids second listener)
	server          *http.Server
	registrationKey string
	selfServerID    *uuid.UUID      // when set, restart for this ID triggers local process restart
	triggerScan     TriggerScanFunc // when set, POST /scan/trigger runs a full scan
	wsHub           *ws.Hub         // WebSocket hub for client connections (main server only)
}

// NewServer creates a new API server. selfServerID is this process's server row ID; when restart is requested for it, the process will restart itself.
// triggerScan is optional; when set, allows HTTP-triggered full library scan (used by Rescan in UI).
// trackerPort is the BitTorrent tracker port on the main server; when non-zero, GET /torrents/:id/file rewrites the announce URL to use the request host so clients reach the tracker.
func NewServer(database *db.DB, port int, registrationKey string, selfServerID *uuid.UUID, triggerScan TriggerScanFunc, trackerPort int) *Server {
	s := &Server{
		router:          mux.NewRouter(),
		database:        database,
		db:              database.DB,
		port:            port,
		trackerPort:     trackerPort,
		registrationKey: registrationKey,
		selfServerID:    selfServerID,
		triggerScan:     triggerScan,
	}

	s.setupRoutes()
	return s
}

// RegisterTracker sets the BitTorrent tracker handler for /announce (route is registered in setupRoutes so it wins over the catch-all).
// Use this on the main server to avoid binding a second port and prevent "address already in use" conflicts.
func (s *Server) RegisterTracker(handler http.Handler) {
	s.trackerHandler = handler
}

// RegisterWebSocketHub sets the WebSocket hub for client connections (main server only)
func (s *Server) RegisterWebSocketHub(hub *ws.Hub) {
	s.wsHub = hub
}

// GetWebSocketHub returns the WebSocket hub
func (s *Server) GetWebSocketHub() *ws.Hub {
	return s.wsHub
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// CORS first so every response (including OPTIONS preflight and 404) has CORS headers
	s.router.Use(s.corsMiddleware)

	// BitTorrent tracker at /announce (must be before PathPrefix("/") so it matches first)
	s.router.HandleFunc("/announce", func(w http.ResponseWriter, r *http.Request) {
		if s.trackerHandler != nil {
			s.trackerHandler.ServeHTTP(w, r)
		} else {
			http.NotFound(w, r)
		}
	}).Methods("GET")

	// Handle CORS preflight for all paths first (so OPTIONS always gets CORS headers)
	s.router.Methods("OPTIONS").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Server-ID, X-MAC-Address")
		w.WriteHeader(http.StatusOK)
	})

	// Release repository - serve release packages (BEFORE api routes so CORS is already applied)
	releasesDir := "/home/appbox/DCPCLOUDAPP/releases"
	s.router.PathPrefix("/releases/").Handler(
		http.StripPrefix("/releases/",
			http.FileServer(http.Dir(releasesDir))),
	)

	// Quick install script endpoint - serve the one-command installer
	s.router.HandleFunc("/install", s.handleInstallScript).Methods("GET")

	// WebSocket endpoint for client connections
	s.router.HandleFunc("/ws", s.handleWebSocket).Methods("GET")

	// API v1 routes
	api := s.router.PathPrefix("/api/v1").Subrouter()

	// Apply middleware
	api.Use(s.loggingMiddleware)
	api.Use(s.corsMiddleware)
	api.Use(s.userAuthMiddleware)

	// User authentication routes (public - no session required)
	api.HandleFunc("/auth/login", s.handleLogin).Methods("POST")
	api.HandleFunc("/auth/logout", s.handleLogout).Methods("POST")
	api.HandleFunc("/auth/session", s.handleSessionCheck).Methods("GET")

	// Health check
	api.HandleFunc("/health", s.handleHealth).Methods("GET")

	// Scan (trigger and status) - used by this server and by main server when proxying to sites
	api.HandleFunc("/scan/trigger", s.handleScanTrigger).Methods("POST")
	api.HandleFunc("/scan/status", s.handleScanStatus).Methods("GET")

	// Server routes
	api.HandleFunc("/servers", s.handleListServers).Methods("GET")
	api.HandleFunc("/servers/register", s.handleRegisterServer).Methods("POST") // No auth required for initial registration
	api.HandleFunc("/servers/{id}", s.handleUpdateServer).Methods("PUT")        // Update server config (auth/deauth)
	api.HandleFunc("/servers/{id}", s.handleDeleteServer).Methods("DELETE")     // Delete server

	// Protected server routes (require authorization)
	apiAuth := api.PathPrefix("/servers/{id}").Subrouter()
	apiAuth.Use(s.authorizationMiddleware)
	apiAuth.HandleFunc("/heartbeat", s.handleHeartbeat).Methods("POST")
	apiAuth.HandleFunc("/inventory", s.handleUpdateInventory).Methods("POST")
	apiAuth.HandleFunc("/dcps", s.handleGetServerDCPs).Methods("GET")
	apiAuth.HandleFunc("/pending-action", s.handlePendingAction).Methods("GET")
	apiAuth.HandleFunc("/pending-transfers", s.handlePendingTransfers).Methods("GET")
	apiAuth.HandleFunc("/transfer-commands", s.handleTransferCommands).Methods("GET")
	apiAuth.HandleFunc("/transfer-command-ack", s.handleTransferCommandAck).Methods("POST")
	apiAuth.HandleFunc("/content-commands", s.handleContentCommands).Methods("GET")
	apiAuth.HandleFunc("/content-command-ack", s.handleContentCommandAck).Methods("POST")
	apiAuth.HandleFunc("/action-done", s.handleActionDone).Methods("POST")
	apiAuth.HandleFunc("/torrent-status", s.handleTorrentStatus).Methods("POST")
	apiAuth.HandleFunc("/nat-check", s.handleNATCheck).Methods("GET")
	apiAuth.HandleFunc("/torrent-queue/claim", s.handleClaimTorrentQueue).Methods("POST")
	apiAuth.HandleFunc("/hash-check", s.handleHashCheck).Methods("POST")
	apiAuth.HandleFunc("/dcp-metadata", s.handleDCPMetadata).Methods("POST")
	apiAuth.HandleFunc("/missing-torrents", s.handleMissingTorrents).Methods("GET")
	apiAuth.HandleFunc("/canonical-xml", s.handleGetCanonicalXML).Methods("POST")

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
	api.HandleFunc("/torrents/{info_hash}/peer-status", s.handleTorrentPeerStatus).Methods("GET")
	api.HandleFunc("/torrents/{info_hash}/seeders", s.handleRegisterSeeder).Methods("POST")
	api.HandleFunc("/tracker/live", s.handleTrackerLive).Methods("GET")

	// Torrent stats routes - detailed per-server stats
	api.HandleFunc("/servers/{id}/torrent-stats", s.handleGetServerTorrentStats).Methods("GET")
	api.HandleFunc("/torrent-stats/all", s.handleGetAllServersTorrentStats).Methods("GET")

	// Torrent queue routes
	api.HandleFunc("/torrent-queue", s.handleListTorrentQueue).Methods("GET")
	api.HandleFunc("/torrent-queue/{id}", s.handleUpdateQueuePosition).Methods("PUT")
	api.HandleFunc("/torrent-queue/{id}/retry", s.handleRetryQueueItem).Methods("POST")
	api.HandleFunc("/torrent-queue/{id}/cancel", s.handleCancelQueueItem).Methods("POST")
	api.HandleFunc("/torrent-queue/clear-completed", s.handleClearCompletedQueue).Methods("POST")

	// Package content management routes
	api.HandleFunc("/packages/{id}/server-status", s.handleGetPackageServerStatus).Methods("GET")
	api.HandleFunc("/content-commands", s.handleCreateContentCommand).Methods("POST")

	// Transfer routes
	api.HandleFunc("/transfers", s.handleListTransfers).Methods("GET")
	api.HandleFunc("/transfers", s.handleCreateTransfer).Methods("POST")
	api.HandleFunc("/transfers/{id}", s.handleGetTransfer).Methods("GET")
	api.HandleFunc("/transfers/{id}", s.handleUpdateTransfer).Methods("PUT")
	api.HandleFunc("/transfers/{id}", s.handleDeleteTransfer).Methods("DELETE")
	api.HandleFunc("/transfers/{id}/retry", s.handleRetryTransfer).Methods("POST")
	api.HandleFunc("/transfers/{id}/pause", s.handlePauseTransfer).Methods("POST")
	api.HandleFunc("/transfers/{id}/resume", s.handleResumeTransfer).Methods("POST")

	// Software version routes
	api.HandleFunc("/versions", s.handleListVersions).Methods("GET")
	api.HandleFunc("/versions", s.handleRegisterVersion).Methods("POST")
	api.HandleFunc("/builds", s.handleCreateBuild).Methods("POST")
	api.HandleFunc("/versions/latest", s.handleGetLatestVersion).Methods("GET")
	api.HandleFunc("/servers/{id}/upgrade", s.handleTriggerUpgrade).Methods("POST")
	api.HandleFunc("/servers/{id}/restart", s.handleRestartServer).Methods("POST")

	// Log ingestion route (receives logs from client servers)
	api.HandleFunc("/logs/ingest", s.handleLogIngest).Methods("POST")
	api.HandleFunc("/servers/{id}/rescan", s.handleRescanServer).Methods("POST")
	api.HandleFunc("/servers/{id}/scan-status", s.handleServerScanStatus).Methods("GET")

	// WebSocket client management routes
	api.HandleFunc("/websocket/clients", s.handleListWebSocketClients).Methods("GET")
	api.HandleFunc("/servers/{id}/ws-status", s.handleGetWebSocketClientStatus).Methods("GET")
	api.HandleFunc("/servers/{id}/send-command", s.handleSendServerCommand).Methods("POST")
	api.HandleFunc("/servers/{id}/delete-content", s.handleDeleteContent).Methods("POST")

	// Server settings routes
	api.HandleFunc("/servers/{id}/settings", s.handleGetServerSettings).Methods("GET")
	api.HandleFunc("/servers/{id}/settings", s.handleUpdateServerSettings).Methods("PUT")
	api.HandleFunc("/servers/{id}/library-locations", s.handleAddLibraryLocation).Methods("POST")
	api.HandleFunc("/servers/{id}/library-locations/{location_id}", s.handleUpdateLibraryLocation).Methods("PUT")
	api.HandleFunc("/servers/{id}/library-locations/{location_id}", s.handleDeleteLibraryLocation).Methods("DELETE")

	// RosettaBridge ingestion status routes
	api.HandleFunc("/servers/{id}/ingestion-status", s.handleGetIngestionStatus).Methods("GET")
	api.HandleFunc("/servers/{id}/ingestion-status", s.handleReportIngestion).Methods("POST")

	// Authorization routes
	api.HandleFunc("/servers/{id}/auth-status", s.handleAuthStatus).Methods("GET")

	// Server activity routes (live activity dashboard)
	api.HandleFunc("/server-activities", s.handleGetAllServerActivities).Methods("GET")
	api.HandleFunc("/servers/{id}/activities", s.handleGetServerActivity).Methods("GET")

	// Admin
	api.HandleFunc("/admin/db-reset", s.handleAdminDBReset).Methods("POST")

	// User management routes (admin only, enforced in handlers)
	api.HandleFunc("/users", s.handleListUsers).Methods("GET")
	api.HandleFunc("/users", s.handleCreateUser).Methods("POST")
	api.HandleFunc("/users/{id}", s.handleUpdateUser).Methods("PUT")
	api.HandleFunc("/users/{id}/password", s.handleChangeUserPassword).Methods("PUT")
	api.HandleFunc("/users/{id}", s.handleDeleteUser).Methods("DELETE")

	// Role/permission routes (admin only, enforced in handlers)
	api.HandleFunc("/roles", s.handleListRoles).Methods("GET")
	api.HandleFunc("/roles/{role}/permissions", s.handleUpdateRolePermissions).Methods("PUT")

	// Activity log routes (admin only, enforced in handlers)
	api.HandleFunc("/activity-logs", s.handleListActivityLogs).Methods("GET")
	api.HandleFunc("/activity-logs/stats", s.handleGetActivityLogStats).Methods("GET")

	// Serve static files for web UI with SPA fallback (must be last to not conflict with API routes)
	webDir := filepath.Join(filepath.Dir(filepath.Dir(os.Args[0])), "web")
	s.router.PathPrefix("/").Handler(spaHandler{staticDir: webDir})

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

// spaHandler serves static files and falls back to index.html for SPA routes
type spaHandler struct {
	staticDir string
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Try to serve the requested file
	path := filepath.Join(h.staticDir, r.URL.Path)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		// File doesn't exist or is a directory → serve index.html (SPA fallback)
		http.ServeFile(w, r, filepath.Join(h.staticDir, "index.html"))
		return
	}
	// Serve the actual static file
	http.FileServer(http.Dir(h.staticDir)).ServeHTTP(w, r)
}
