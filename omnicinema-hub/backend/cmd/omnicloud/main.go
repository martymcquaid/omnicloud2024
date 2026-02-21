package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/api"
	"github.com/omnicloud/omnicloud/internal/config"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/scanner"
	torrentpkg "github.com/omnicloud/omnicloud/internal/torrent"
	"github.com/omnicloud/omnicloud/internal/watcher"
)

func main() {
	log.Println("Starting OmniCloud DCP Manager...")

	// Optional file logging (for live tail -f)
	// Example: OMNICLOUD_LOG_FILE=/var/log/omnicloud.log
	if logPath := os.Getenv("OMNICLOUD_LOG_FILE"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("Warning: failed to open log file %q: %v", logPath, err)
		} else {
			defer f.Close()
			log.SetOutput(io.MultiWriter(os.Stdout, f))
			log.Printf("Logging to %s", logPath)
		}
	}

	// Load configuration
	workDir, _ := os.Getwd()
	configPath := filepath.Join(workDir, "auth.config")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Try parent directory
		configPath = filepath.Join(filepath.Dir(workDir), "auth.config")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Configuration loaded:")
	log.Printf("  Database: %s@%s:%d/%s", cfg.DBUser, cfg.DBHost, cfg.DBPort, cfg.DBName)
	log.Printf("  Scan Path: %s", cfg.ScanPath)
	log.Printf("  API Port: %d", cfg.APIPort)
	if cfg.ScanIntervalMinutes > 0 {
		log.Printf("  Scan Interval: %d minutes (automatic DCP discovery)", cfg.ScanIntervalMinutes)
	} else {
		log.Printf("  Scan Interval: %d hours", cfg.ScanInterval)
	}
	log.Printf("  Server Mode: %s", cfg.ServerMode)
	log.Printf("  Tracker Port: %d", cfg.TrackerPort)
	log.Printf("  Torrent Data Dir: %s", cfg.TorrentDataDir)
	if cfg.IsClient() {
		log.Printf("  Main Server URL: %s", cfg.MainServerURL)
	}

	// Connect to database
	database, err := db.Connect(cfg.ConnectionString())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	log.Println("Database connected successfully")

	// Run migrations
	if err := runMigrations(database); err != nil {
		log.Printf("Warning: migration errors occurred: %v", err)
		// Continue anyway - migrations may have already been run
	}

	// Register this server in the database
	serverID, err := registerServer(database, cfg)
	if err != nil {
		log.Fatalf("Failed to register server: %v", err)
	}

	log.Printf("Server registered with ID: %s", serverID)

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Ensure torrent data directory exists
	if err := os.MkdirAll(cfg.TorrentDataDir, 0755); err != nil {
		log.Fatalf("Failed to create torrent data directory: %v", err)
	}

	// Initialize torrent client
	trackerURL := cfg.PublicTrackerURL
	if trackerURL == "" {
		trackerURL = fmt.Sprintf("http://%s:%d/announce", cfg.DBHost, cfg.TrackerPort)
		if cfg.IsClient() && cfg.MainServerURL != "" {
			// Extract hostname from main server URL for tracker
			trackerURL = strings.Replace(cfg.MainServerURL, fmt.Sprintf(":%d", cfg.APIPort), fmt.Sprintf(":%d", cfg.TrackerPort), 1) + "/announce"
		}
	}
	log.Printf("  Tracker URL: %s", trackerURL)

	torrentCfg := torrent.NewDefaultClientConfig()
	torrentCfg.DataDir = cfg.TorrentDataDir
	torrentCfg.Seed = true    // CRITICAL: Enable seeding mode so client uploads data to peers
	torrentCfg.NoDHT = true   // Disable DHT for private tracker
	torrentCfg.DisableUTP = true
	torrentCfg.ListenPort = cfg.TorrentDataPort
	log.Printf("  Torrent data port: %d", cfg.TorrentDataPort)
	log.Printf("  Seed mode: enabled")

	// Set public IP so the torrent client advertises a reachable address
	if cfg.PublicIP != "" {
		publicIP := net.ParseIP(cfg.PublicIP)
		if publicIP != nil {
			if ipv4 := publicIP.To4(); ipv4 != nil {
				torrentCfg.PublicIp4 = ipv4
				log.Printf("  Public IPv4 for torrent client: %s", cfg.PublicIP)
			} else {
				torrentCfg.PublicIp6 = publicIP
				log.Printf("  Public IPv6 for torrent client: %s", cfg.PublicIP)
			}
		} else {
			log.Printf("  Warning: invalid public_ip %q, ignoring", cfg.PublicIP)
		}
	}

	torrentClient, err := torrentpkg.NewClient(torrentCfg, database.DB, serverID.String())
	if err != nil {
		log.Fatalf("Failed to create torrent client: %v", err)
	}
	defer torrentClient.Close()

	// Set local tracker URL for announces to avoid NAT hairpin
	// The public URL is for .torrent files; the local URL is for the client's own announces
	localTrackerURL := fmt.Sprintf("http://localhost:%d/announce", cfg.TrackerPort)
	torrentClient.SetLocalTrackerURL(localTrackerURL)
	log.Printf("  Local tracker URL: %s", localTrackerURL)
	log.Println("Torrent client initialized")

	// Initialize torrent generator
	generator := torrentpkg.NewGenerator(database.DB, trackerURL, cfg.PieceHashWorkers)

	// Initialize queue manager
	queueManager := torrentpkg.NewQueueManager(database.DB, generator, torrentClient, serverID.String(), 2)
	if cfg.IsClient() && cfg.MainServerURL != "" {
		queueManager.SetMainServerURL(cfg.MainServerURL)
	}
	go queueManager.Start(ctx)
	log.Println("Torrent queue manager started")

	// Start BitTorrent tracker (main server only)
	var tracker *torrentpkg.Tracker
	if cfg.IsMainServer() {
		tracker = torrentpkg.NewTracker(database.DB, 60, cfg.PublicIP)
		go func() {
			addr := fmt.Sprintf(":%d", cfg.TrackerPort)
			if err := tracker.Start(addr); err != nil {
				log.Printf("Tracker error: %v", err)
			}
		}()
		log.Printf("BitTorrent tracker started on port %d", cfg.TrackerPort)
	}

	// Start status reporter (client mode only); pass db so hashing queue progress is reported to main
	if cfg.IsClient() && cfg.MainServerURL != "" {
		reporter := torrentpkg.NewStatusReporter(torrentClient, database.DB, cfg.MainServerURL, serverID.String())
		go reporter.Start(ctx)
		log.Println("Torrent status reporter started")
	}

	// Start ensure-seeding restore and periodic sync (both modes)
	go torrentpkg.EnsureSeedingRestore(ctx, database.DB, torrentClient, serverID.String(), 5*time.Minute)
	go torrentpkg.SyncSeedersToDB(ctx, database.DB, torrentClient, serverID.String(), 30*time.Second)
	log.Println("Started torrent seeding restorer")

	// Create channels for communication
	scanRequests := make(chan string, 100)
	stopChan := make(chan struct{})

	// Start scan handler worker with torrent queue
	scanHandler := watcher.NewScanHandler(database, serverID)
	scanHandler.GetIndexer().SetTorrentQueue(queueManager)
	go scanHandler.StartScanWorker(scanRequests, stopChan)

	// Start filesystem watcher
	fsWatcher, err := watcher.NewWatcher(cfg.ScanPath, scanRequests)
	if err != nil {
		log.Fatalf("Failed to create filesystem watcher: %v", err)
	}

	if err := fsWatcher.Start(); err != nil {
		log.Fatalf("Failed to start filesystem watcher: %v", err)
	}
	defer fsWatcher.Stop()

	// Start periodic scanner (interval: minutes if set, else hours)
	var scanInterval time.Duration
	if cfg.ScanIntervalMinutes > 0 {
		scanInterval = time.Duration(cfg.ScanIntervalMinutes) * time.Minute
	} else {
		scanInterval = time.Duration(cfg.ScanInterval) * time.Hour
	}
	periodicScanner := scanner.NewPeriodicScanner(cfg.ScanPath, scanInterval, database, serverID)
	periodicScanner.GetIndexer().SetTorrentQueue(queueManager)
	periodicScanner.Start()
	defer periodicScanner.Stop()

	// Start API server
	apiServer := api.NewServer(database, cfg.APIPort, cfg.RegistrationKey)
	if tracker != nil {
		apiServer.RegisterTracker(tracker)
	}
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	// When this process is the main server (not a client), periodically update our own last_seen
	// so the Sites UI shows this server as Online. Clients send heartbeat to main; main has no one to send to, so we self-update.
	if !cfg.IsClient() {
		go startSelfHeartbeat(ctx, database, serverID, 2*time.Minute)
		log.Println("Self-heartbeat started (main server will show as Online)")
	}

	// Start client sync and rescan poller if in client mode
	var clientSync *api.ClientSync
	var rescanPoller *api.RescanPoller
	if cfg.IsClient() && cfg.MainServerURL != "" {
		clientSync, err = api.NewClientSync(database, serverID, cfg.MainServerURL, cfg.ServerName, cfg.ServerLocation, cfg.RegistrationKey)
		if err != nil {
			log.Printf("Warning: failed to create client sync: %v", err)
		} else {
			clientSync.Start()
			defer clientSync.Stop()
		}
		rescanPoller = api.NewRescanPoller(database, serverID, cfg.MainServerURL, periodicScanner.RunFullScan)
		rescanPoller.Start()
		defer rescanPoller.Stop()
	}

	log.Println("OmniCloud is running")
	log.Println("Press Ctrl+C to stop")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutdown signal received, stopping OmniCloud...")

	// Cancel context to stop all goroutines
	cancel()

	// Graceful shutdown
	close(stopChan)

	// Stop API server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error shutting down API server: %v", err)
	}

	log.Println("OmniCloud stopped")
}

// runMigrations executes database migrations
func runMigrations(database *db.DB) error {
	log.Println("Running database migrations...")

	workDir, _ := os.Getwd()
	migrationPath := filepath.Join(workDir, "internal", "db", "migrations")

	// If not found, try from project root
	if _, err := os.Stat(migrationPath); os.IsNotExist(err) {
		migrationPath = filepath.Join(filepath.Dir(workDir), "internal", "db", "migrations")
	}

	migrations := []string{
		"001_create_servers.sql",
		"002_create_dcp_packages.sql",
		"003_create_dcp_compositions.sql",
		"004_create_dcp_assets.sql",
		"005_create_inventory.sql",
		"006_add_mac_address.sql",
		"007_create_torrent_tables.sql",
		"012_add_composition_unique.sql",
		"014_server_torrent_status.sql",
		"015_create_torrent_announce_attempts.sql",
		"017_rescan_and_scan_status.sql",
	}

	for _, migration := range migrations {
		migrationFile := filepath.Join(migrationPath, migration)
		log.Printf("  Running migration: %s", migration)

		content, err := ioutil.ReadFile(migrationFile)
		if err != nil {
			return err
		}

		if _, err := database.Exec(string(content)); err != nil {
			log.Printf("  Warning: %s: %v", migration, err)
			// Continue with other migrations
		} else {
			log.Printf("  ✓ %s completed", migration)
		}
	}

	log.Println("Migrations complete")
	return nil
}

// registerServer registers this server in the database
func registerServer(database *db.DB, cfg *config.Config) (uuid.UUID, error) {
	// Check if server already exists
	existing, err := database.GetServerByName(cfg.ServerName)
	if err != nil {
		return uuid.Nil, err
	}

	if existing != nil {
		// Update existing server
		now := time.Now()
		existing.Location = cfg.ServerLocation
		existing.LastSeen = &now
		if err := database.UpsertServer(existing); err != nil {
			return uuid.Nil, err
		}
		log.Printf("Updated existing server: %s", existing.Name)
		return existing.ID, nil
	}

	// Get MAC address for this server
	macAddress, err := getMACAddress()
	if err != nil {
		log.Printf("Warning: could not get MAC address: %v", err)
		macAddress = "unknown"
	}

	// Create new server
	now := time.Now()
	server := &db.Server{
		ID:                  uuid.New(),
		Name:                cfg.ServerName,
		Location:            cfg.ServerLocation,
		APIURL:              "",
		MACAddress:          macAddress,
		RegistrationKeyHash: "",
		IsAuthorized:        true, // Main server is always authorized
		LastSeen:            &now,
		StorageCapacityTB:   0,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := database.UpsertServer(server); err != nil {
		return uuid.Nil, err
	}

	log.Printf("Registered new server: %s (MAC: %s)", server.Name, macAddress)
	return server.ID, nil
}

// startSelfHeartbeat periodically updates this server's last_seen in the database so the main server
// (dcp1 / this process) shows as Online in the Sites UI. Only main server runs this; clients update
// last_seen by sending heartbeat to the main server.
func startSelfHeartbeat(ctx context.Context, database *db.DB, serverID uuid.UUID, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			_, err := database.Exec("UPDATE servers SET last_seen = $1, updated_at = $2 WHERE id = $3",
				now, now, serverID)
			if err != nil {
				log.Printf("Self-heartbeat failed: %v", err)
			}
		}
	}
}

// getMACAddress returns the MAC address of the primary network interface
func getMACAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range interfaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		// Get the hardware address (MAC)
		mac := iface.HardwareAddr.String()
		if mac != "" {
			return strings.ToUpper(mac), nil
		}
	}

	return "", fmt.Errorf("no active network interface found")
}
