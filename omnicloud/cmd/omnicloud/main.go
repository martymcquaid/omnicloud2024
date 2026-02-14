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
	"github.com/omnicloud/omnicloud/pkg/dcp"
	torrentpkg "github.com/omnicloud/omnicloud/internal/torrent"
	"github.com/omnicloud/omnicloud/internal/updater"
	"github.com/omnicloud/omnicloud/internal/watcher"
)

// Version is set at build time via ldflags
var Version = "dev"

const postUpgradeReplacePrefix = "--post-upgrade-replace="

func main() {
	// Handle post-upgrade replace: we are the new binary running from <exe>.new.
	// Replace the old binary and re-exec from the final path.
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, postUpgradeReplacePrefix) {
			targetPath := strings.TrimPrefix(arg, postUpgradeReplacePrefix)
			me, err := os.Executable()
			if err != nil {
				log.Fatalf("post-upgrade: executable path: %v", err)
			}
			if err := os.Rename(me, targetPath); err != nil {
				log.Fatalf("post-upgrade: replace binary: %v", err)
			}
			var cleanArgs []string
			for _, a := range os.Args[1:] {
				if !strings.HasPrefix(a, postUpgradeReplacePrefix) {
					cleanArgs = append(cleanArgs, a)
				}
			}
			cleanArgs = append([]string{targetPath}, cleanArgs...)
			log.Printf("Self-upgrade: binary replaced; re-exec from %s", targetPath)
			if err := syscall.Exec(targetPath, cleanArgs, os.Environ()); err != nil {
				log.Fatalf("post-upgrade: re-exec: %v", err)
			}
			return
		}
	}

	log.Printf("Starting OmniCloud DCP Manager v%s...", Version)

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
	log.Printf("  Scan Interval: %d hours", cfg.ScanInterval)
	log.Printf("  Server Mode: %s", cfg.ServerMode)
	log.Printf("  Tracker Port: %d", cfg.TrackerPort)
	log.Printf("  Torrent Data Dir: %s", cfg.TorrentDataDir)
	log.Printf("  Torrent generation: %d concurrent workers, %d piece-hash workers", cfg.MaxTorrentGenerationWorkers, cfg.PieceHashWorkers)
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
	trackerURL := fmt.Sprintf("http://%s:%d/announce", cfg.DBHost, cfg.TrackerPort)
	if cfg.IsClient() && cfg.MainServerURL != "" {
		// Extract hostname from main server URL for tracker
		trackerURL = strings.Replace(cfg.MainServerURL, fmt.Sprintf(":%d", cfg.APIPort), fmt.Sprintf(":%d", cfg.TrackerPort), 1) + "/announce"
	}

	torrentCfg := torrent.NewDefaultClientConfig()
	torrentCfg.DataDir = cfg.TorrentDataDir
	torrentCfg.NoDHT = true // Disable DHT for private tracker
	torrentCfg.DisableUTP = true
	torrentCfg.ListenPort = 0 // auto-pick free port

	torrentClient, err := torrentpkg.NewClient(torrentCfg, database.DB, serverID.String())
	if err != nil {
		log.Fatalf("Failed to create torrent client: %v", err)
	}
	defer torrentClient.Close()
	log.Println("Torrent client initialized")

	// Initialize torrent generator
	generator := torrentpkg.NewGenerator(database.DB, trackerURL, cfg.PieceHashWorkers)

	// Initialize queue manager (use config for max concurrent generations).
	// Single instance per process: main server has one; each client site runs its own with its own server_id.
	queueManager := torrentpkg.NewQueueManager(database.DB, generator, torrentClient, serverID.String(), cfg.MaxTorrentGenerationWorkers)
	go queueManager.Start(ctx)
	log.Printf("Torrent queue manager started (max %d concurrent generations)", cfg.MaxTorrentGenerationWorkers)

	// Start BitTorrent tracker (main server only)
	var tracker *torrentpkg.Tracker
	if cfg.IsMainServer() {
		tracker = torrentpkg.NewTracker(database.DB, 60)
		go func() {
			addr := fmt.Sprintf(":%d", cfg.TrackerPort)
			if err := tracker.Start(addr); err != nil {
				log.Printf("Tracker error: %v", err)
			}
		}()
		log.Printf("BitTorrent tracker started on port %d", cfg.TrackerPort)
	}

	// Start status reporter (client mode only)
	if cfg.IsClient() && cfg.MainServerURL != "" {
		reporter := torrentpkg.NewStatusReporter(torrentClient, database.DB, cfg.MainServerURL, serverID.String())
		go reporter.Start(ctx)
		log.Println("Torrent status reporter started")
	}

	// Create channels for communication
	scanRequests := make(chan string, 100)
	stopChan := make(chan struct{})

	// Start scan handler worker with torrent queue
	scanHandler := watcher.NewScanHandler(database, serverID)
	scanHandler.GetIndexer().SetTorrentQueue(queueManager)
	
	// For client mode, enable torrent downloading from main server
	if cfg.IsClient() && cfg.MainServerURL != "" {
		macAddress, _ := dcp.GetMACAddress()
		scanHandler.GetIndexer().SetClientMode(cfg.MainServerURL, macAddress)
	}
	
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

	// Start periodic scanner
	periodicScanner := scanner.NewPeriodicScanner(cfg.ScanPath, cfg.ScanInterval, database, serverID)
	periodicScanner.GetIndexer().SetTorrentQueue(queueManager)
	
	// For client mode, enable torrent downloading from main server
	if cfg.IsClient() && cfg.MainServerURL != "" {
		macAddress, _ := dcp.GetMACAddress()
		periodicScanner.GetIndexer().SetClientMode(cfg.MainServerURL, macAddress)
	}
	periodicScanner.Start()
	defer periodicScanner.Stop()

	// Start API server (pass our server ID and scan trigger for Rescan in UI)
	triggerScan := func() { go periodicScanner.RunFullScan() }
	apiServer := api.NewServer(database, cfg.APIPort, cfg.RegistrationKey, &serverID, triggerScan)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	// Start client sync if in client mode
	var clientSync *api.ClientSync
	if cfg.IsClient() && cfg.MainServerURL != "" {
		clientSync, err = api.NewClientSync(database, serverID, cfg.MainServerURL, cfg.ServerName, cfg.ServerLocation, cfg.RegistrationKey, Version, cfg.ScanPath)
		if err != nil {
			log.Printf("Warning: failed to create client sync: %v", err)
		} else {
			clientSync.Start()
			defer clientSync.Stop()
		}

		// Start update agent for automatic upgrades (polls main server for restart/upgrade)
		macAddress, _ := dcp.GetMACAddress()
		updateAgent := updater.NewAgent(database, serverID, cfg.MainServerURL, macAddress, Version)
		go updateAgent.Start()
		defer updateAgent.Stop()
		log.Println("Update agent started")

		// Start transfer processor for automatic content downloads
		transferProcessor := torrentpkg.NewTransferProcessor(torrentClient, cfg.MainServerURL, serverID.String(), macAddress)
		go transferProcessor.Start(ctx)
		defer transferProcessor.Stop()
		log.Println("Transfer processor started")
	} else if cfg.IsMainServer() {
		// Main servers also need periodic storage updates
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			
			for {
				select {
				case <-ticker.C:
					// Calculate current storage capacity
					var storageCapacityTB float64
					if cfg.ScanPath != "" {
						totalSize, err := dcp.CalculateDirectorySize(cfg.ScanPath)
						if err != nil {
							log.Printf("Warning: Could not calculate storage size: %v", err)
						} else {
							storageCapacityTB = float64(totalSize) / (1024 * 1024 * 1024 * 1024)
						}
					}

					// Get current package count
					packageCount, _ := database.CountDCPPackages()

					// Update server record
					_, err := database.DB.Exec(`
						UPDATE servers 
						SET storage_capacity_tb = $1, 
						    software_version = $2,
						    last_seen = $3
						WHERE id = $4
					`, storageCapacityTB, Version, time.Now(), serverID)
					if err != nil {
						log.Printf("Error updating server storage: %v", err)
					} else {
						log.Printf("Storage updated: %.2f TB, %d packages", storageCapacityTB, packageCount)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		log.Println("Storage updater started")
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
		"008_add_torrent_queue_total_size.sql",
		"009_torrent_queue_total_size_bigint.sql",
		"010_add_software_versions.sql",
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

	// Calculate storage capacity from scan path
	var storageCapacityTB float64
	if cfg.ScanPath != "" {
		totalSize, err := dcp.CalculateDirectorySize(cfg.ScanPath)
		if err != nil {
			log.Printf("Warning: Could not calculate storage size: %v", err)
		} else {
			storageCapacityTB = float64(totalSize) / (1024 * 1024 * 1024 * 1024)
			log.Printf("Calculated storage: %.2f TB from %s", storageCapacityTB, cfg.ScanPath)
		}
	}

	// Detect public IP for API URL
	publicIP := dcp.GetPublicIP()
	apiURL := fmt.Sprintf("http://%s:%d", cfg.ServerName, cfg.APIPort)
	if publicIP != "" && publicIP != "127.0.0.1" {
		apiURL = fmt.Sprintf("http://%s:%d", publicIP, cfg.APIPort)
		log.Printf("Detected public IP: %s", publicIP)
	}

	if existing != nil {
		// Update existing server (refresh last_seen, storage, and api_url)
		now := time.Now()
		existing.Location = cfg.ServerLocation
		existing.LastSeen = &now
		existing.APIURL = apiURL
		existing.StorageCapacityTB = storageCapacityTB
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
		APIURL:              apiURL,
		MACAddress:          macAddress,
		RegistrationKeyHash: "",
		IsAuthorized:        true, // Main server is always authorized
		LastSeen:            &now,
		StorageCapacityTB:   storageCapacityTB,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := database.UpsertServer(server); err != nil {
		return uuid.Nil, err
	}

	log.Printf("Registered new server: %s (MAC: %s, Storage: %.2f TB)", server.Name, macAddress, storageCapacityTB)
	return server.ID, nil
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
