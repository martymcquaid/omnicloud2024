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
	"github.com/omnicloud/omnicloud/internal/relay"
	"github.com/omnicloud/omnicloud/internal/scanner"
	"github.com/omnicloud/omnicloud/pkg/dcp"
	torrentpkg "github.com/omnicloud/omnicloud/internal/torrent"
	"github.com/omnicloud/omnicloud/internal/updater"
	"github.com/omnicloud/omnicloud/internal/watcher"
	ws "github.com/omnicloud/omnicloud/internal/websocket"
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

	// Initialize relay-specific log file (relay.log in working directory)
	// All relay system logs are written to both main log and this file.
	relay.InitRelayLog(".")
	defer relay.CloseRelayLog()

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
	log.Printf("  Relay: enabled=%v port=%d max_sessions=%d", cfg.RelayEnabled, cfg.RelayPort, cfg.RelayMaxSessions)
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

	// Seed default admin user (only if no users exist)
	if err := database.SeedDefaultUser("martyn", "Cinema200"); err != nil {
		log.Printf("Warning: could not seed default user: %v", err)
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

	// Initialize torrent client.
	// For the main server, use the public IP in the tracker URL embedded in .torrent files
	// so external clients can reach the tracker. Fall back to DBHost if detection fails.
	trackerHost := cfg.DBHost
	if cfg.IsMainServer() {
		if pubIP := dcp.GetPublicIP(); pubIP != "" && pubIP != "127.0.0.1" {
			trackerHost = pubIP
		}
	}
	trackerURL := fmt.Sprintf("http://%s:%d/announce", trackerHost, cfg.TrackerPort)
	if cfg.IsClient() && cfg.MainServerURL != "" {
		// Extract hostname from main server URL for tracker.
		// Guard against TrackerPort == 0 (not set in client config): fall back to default port 10851.
		trackerPort := cfg.TrackerPort
		if trackerPort == 0 {
			trackerPort = 10851
			log.Printf("WARNING: tracker_port not set in config, defaulting to 10851 for tracker URL")
		}
		trackerURL = strings.Replace(cfg.MainServerURL, fmt.Sprintf(":%d", cfg.APIPort), fmt.Sprintf(":%d", trackerPort), 1) + "/announce"
	}
	log.Printf("Tracker announce URL: %s", trackerURL)

	torrentCfg := torrent.NewDefaultClientConfig()
	torrentCfg.DataDir = cfg.TorrentDataDir
	torrentCfg.Seed = true // Required: without this the library won't upload and stops announcing once pieces are verified
	torrentCfg.NoDHT = true // Disable DHT for private tracker
	torrentCfg.DisableUTP = true
	torrentCfg.ListenPort = cfg.TorrentDataPort // 0 = auto-pick; set torrent_data_port in config for stable port
	// torrentCfg.Debug = true // Disabled: floods logs with per-piece completion messages
	if cfg.TorrentDataPort > 0 {
		log.Printf("Torrent data port: %d (from config)", cfg.TorrentDataPort)
	} else {
		log.Printf("Torrent data port: auto-pick (set torrent_data_port in config for stable port)")
	}

	// Per-torrent BoltDB completion cache directory (persists piece verification across restarts)
	completionDir := filepath.Join(cfg.TorrentDataDir, "completion-cache")

	torrentClient, err := torrentpkg.NewClient(torrentCfg, database.DB, serverID.String(), completionDir, cfg.ScanPath, trackerURL, cfg.TrackerPort)
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

	// Start BitTorrent tracker (main server only) — must start before SeedExisting
	// so the tracker is ready when torrents register as seeders.
	var tracker *torrentpkg.Tracker
	if cfg.IsMainServer() {
		// For loopback announcers (main server seeding on same host), advertise a reachable IP.
		// Prefer env, then auto-detect public IP so static public_ip in config is not required.
		announceHost := os.Getenv("OMNICLOUD_TRACKER_ANNOUNCE_HOST")
		if announceHost == "" {
			announceHost = dcp.GetPublicIP()
			if announceHost != "" {
				log.Printf("Torrent seeding: using auto-detected public IP %s for tracker peer replies", announceHost)
			}
		}
		tracker = torrentpkg.NewTracker(database.DB, 60, announceHost)
		torrentClient.SetTracker(tracker)
		go func() {
			addr := fmt.Sprintf(":%d", cfg.TrackerPort)
			if err := tracker.Start(addr); err != nil {
				log.Printf("Tracker error: %v", err)
			}
		}()
		log.Printf("BitTorrent tracker started on port %d", cfg.TrackerPort)

		// Re-register seeders with tracker periodically to prevent cleanup expiry
		go torrentClient.StartSeederMaintenance(ctx)
		log.Println("Seeder maintenance started")

		// Start relay server for NAT traversal (bridges connections between NATted peers)
		if cfg.RelayEnabled {
			relayServer := relay.NewServer(cfg.RelayPort, cfg.RelayMaxSessions)
			go func() {
				if err := relayServer.Start(ctx); err != nil {
					log.Printf("Relay server error: %v", err)
				}
			}()
			log.Printf("Relay server started on port %d (max %d sessions)", cfg.RelayPort, cfg.RelayMaxSessions)

			// Tell tracker about the relay server so it can include relay info in announce responses
			relayHost := announceHost
			if relayHost == "" {
				relayHost = trackerHost
			}
			tracker.SetRelayInfo(relayHost, cfg.RelayPort)
		}
	}

	// Repair piece completion data: delete any completed=false entries that may have been
	// written by duplicate processes (race condition). The library will re-verify from disk.
	torrentClient.RepairPieceCompletion()

	// Resume seeding all existing torrents for this server.
	// This runs synchronously so seeders are registered with the tracker
	// before clients can poll for transfers and find zero peers.
	torrentClient.SeedExisting()

	// Dump tracker swarm state after seeding is registered (main server only)
	if tracker != nil {
		tracker.DumpSwarms()
	}

	// Start seed health monitor — periodically checks ALL torrents are still actively seeding
	// and logs comprehensive diagnostics. Detects when the library silently stops seeding.
	go torrentClient.StartSeedHealthMonitor(ctx)

	// Start file integrity watcher — detects when torrent data is deleted from disk
	go torrentClient.StartIntegrityWatcher(ctx)

	// Status reporter is started below in the client block (needs macAddress for auth)

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
		log.Printf("WARNING: Failed to create filesystem watcher: %v (continuing without live file watching)", err)
	} else {
		if err := fsWatcher.Start(); err != nil {
			log.Printf("WARNING: Failed to start filesystem watcher: %v (continuing without live file watching)", err)
		} else {
			defer fsWatcher.Stop()
		}
	}

	// Create periodic scanner (Start() is called AFTER settings client is configured below)
	periodicScanner := scanner.NewPeriodicScanner(cfg.ScanPath, cfg.ScanInterval, database, serverID)
	periodicScanner.GetIndexer().SetTorrentQueue(queueManager)
	defer periodicScanner.Stop()

	// Start API server (pass our server ID and scan trigger for Rescan in UI)
	triggerScan := func() { go periodicScanner.RunFullScan() }
	apiServer := api.NewServer(database, cfg.APIPort, cfg.RegistrationKey, &serverID, triggerScan, cfg.TrackerPort)
	if tracker != nil {
		apiServer.RegisterTracker(tracker)
	}

	// Initialize WebSocket hub for main server
	var wsHub *ws.Hub
	if cfg.IsMainServer() {
		wsHub = ws.NewHub(database.DB)
		go wsHub.Run()
		apiServer.RegisterWebSocketHub(wsHub)
		log.Println("WebSocket hub started for client connections")
	}

	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	// Start client sync if in client mode
	var clientSync *api.ClientSync
	var wsClient *ws.ClientConnector
	if cfg.IsClient() && cfg.MainServerURL != "" {
		clientSync, err = api.NewClientSync(database, serverID, cfg.MainServerURL, cfg.ServerName, cfg.ServerLocation, cfg.RegistrationKey, Version, cfg.ScanPath)
		if err != nil {
			log.Printf("Warning: failed to create client sync: %v", err)
		} else {
			// Register with main server synchronously to get the correct remote server ID
			// This ID is needed by the update agent and reporter for auth headers
			remoteServerID, regErr := clientSync.RegisterWithMainServer()
			if regErr != nil {
				log.Printf("Warning: failed to register with main server: %v", regErr)
			} else {
				log.Printf("Registered with main server, remote server ID: %s", remoteServerID)
			}
			clientSync.Start()
			defer clientSync.Stop()

			// Use the remote server ID (from main server) for all API calls to main server
			macAddress := clientSync.MACAddress()
			remoteID := clientSync.ServerID()

			// Enable hash orchestration: before hashing, check with main server
			// if another server is already generating the torrent for this package
			queueManager.SetMainServerConfig(cfg.MainServerURL, remoteID.String(), macAddress)

			// Create settings client to fetch library locations from main server
			settingsClient := api.NewSettingsClient(cfg.MainServerURL, remoteID.String(), macAddress)
			periodicScanner.SetSettingsClient(settingsClient)
			log.Println("Settings client configured - will fetch library locations from main server")

			// Wire settings client into the indexer for CPL-based deduplication.
			// When the same DCP is found under a different ASSETMAP UUID (RosettaBridge
			// delivers fresh ASETMAPs per site), the indexer will recognise it via CPL UUID,
			// fetch the canonical XML files from the main server, and co-seed the existing torrent.
			periodicScanner.GetIndexer().SetSettingsClient(settingsClient)
			scanHandler.GetIndexer().SetSettingsClient(settingsClient)

			// Fetch torrent download location from main server
			// This overrides the config file path for where torrents are downloaded
			torrentDownloadDir := cfg.ScanPath // Default: fall back to scan path from config
			if tdl, err := settingsClient.GetTorrentDownloadLocation(); err != nil {
				log.Printf("Warning: could not fetch torrent download location from main server: %v", err)
				log.Printf("Using config file scan path for downloads: %s", torrentDownloadDir)
			} else if tdl != "" {
				torrentDownloadDir = tdl
				log.Printf("Using torrent download location from main server: %s", torrentDownloadDir)
				// Ensure the directory exists
				if mkErr := os.MkdirAll(torrentDownloadDir, 0755); mkErr != nil {
					log.Printf("WARNING: could not create torrent download directory %s: %v", torrentDownloadDir, mkErr)
					log.Printf("WARNING: falling back to config scan_path: %s", cfg.ScanPath)
					torrentDownloadDir = cfg.ScanPath
				} else {
					// Verify the directory is actually writable (not on a read-only filesystem)
					testFile := filepath.Join(torrentDownloadDir, ".omnicloud-write-test")
					if wErr := ioutil.WriteFile(testFile, []byte("test"), 0644); wErr != nil {
						log.Printf("WARNING: torrent download directory %s is NOT writable: %v", torrentDownloadDir, wErr)
						log.Printf("WARNING: This usually means the filesystem is mounted read-only.")
						log.Printf("WARNING: Run: sudo chown -R omnicloud:omnicloud %s && sudo chmod -R 755 %s", torrentDownloadDir, torrentDownloadDir)
						log.Printf("WARNING: Or remount the filesystem read-write: sudo mount -o remount,rw <device> %s", torrentDownloadDir)
						log.Printf("WARNING: falling back to config scan_path: %s", cfg.ScanPath)
						torrentDownloadDir = cfg.ScanPath
					} else {
						os.Remove(testFile) // Clean up test file
						log.Printf("Verified torrent download directory is writable: %s", torrentDownloadDir)
					}
				}
				// Update the torrent client's download path for ResumeDownloads
				torrentClient.SetDownloadPath(torrentDownloadDir)
			}

			// Set up the shadow XML base directory for canonical XML co-seeding.
			// Canonical XML files (ASSETMAP, PKL, etc.) are stored here, separate from
			// the RosettaBridge library, so RosettaBridge is not disturbed.
			shadowXMLBase := filepath.Join(torrentDownloadDir, "canonical-xml")
			if mkErr := os.MkdirAll(shadowXMLBase, 0755); mkErr != nil {
				log.Printf("Warning: could not create canonical XML shadow dir %s: %v", shadowXMLBase, mkErr)
			} else {
				periodicScanner.GetIndexer().SetShadowXMLBase(shadowXMLBase)
				scanHandler.GetIndexer().SetShadowXMLBase(shadowXMLBase)
				log.Printf("Canonical XML shadow directory: %s", shadowXMLBase)
			}

			// Start update agent for automatic upgrades (polls main server for restart/upgrade)
			updateAgent := updater.NewAgent(database, remoteID, cfg.MainServerURL, macAddress, Version)
			go updateAgent.Start()
			defer updateAgent.Stop()
			log.Println("Update agent started")

			// Start torrent status reporter (sends auth headers to main server)
			reporter := torrentpkg.NewStatusReporter(torrentClient, database.DB, cfg.MainServerURL, remoteID.String(), macAddress)
			go reporter.Start(ctx)
			log.Println("Torrent status reporter started")

			// Start transfer processor to pick up queued downloads from main server
			// Uses torrent download location from main server settings (not config file)
			transferProcessor := torrentpkg.NewTransferProcessor(
				torrentClient,
				cfg.MainServerURL,
				remoteID.String(),
				macAddress,
				torrentDownloadDir,
			)

			// Wire up error reporting so download errors are reported to the main server
			torrentClient.SetErrorReporter(func(transferID, status, errorMessage string) error {
				return transferProcessor.ReportTransferError(transferID, status, errorMessage)
			})

			go transferProcessor.Start(ctx)
			defer transferProcessor.Stop()
			log.Println("Transfer processor started")

			// Resume any in-progress downloads from before restart
			go torrentClient.ResumeDownloads()

			// Start RosettaBridge ingestion detector
			ingestionDetector := torrentpkg.NewIngestionDetector(torrentClient, database.DB, remoteID.String(), settingsClient)
			go ingestionDetector.Start(ctx)
			log.Println("RosettaBridge ingestion detector started")

			// --- Relay / NAT Traversal ---
			// If relay is enabled, set up NAT detection, relay dialer, and relay client
			// so downloads can work even when seeders are behind NAT/firewalls.
			if cfg.RelayEnabled && cfg.MainServerURL != "" {
				// Derive relay server address from main server URL
				relayAddr := deriveRelayAddr(cfg.MainServerURL, cfg.RelayPort)
				log.Printf("[relay] Relay enabled, relay server address: %s", relayAddr)

				// Add relay dialer to the torrent client — tried in parallel with direct TCP.
				// The relay dialer waits 1 second before attempting, so direct connections
				// win when they work. If direct fails (NAT), relay kicks in.
				relayDialer := relay.NewRelayDialer(relayAddr)

				// Register our own listening address with the relay dialer so it never
				// attempts to relay-connect to itself. The anacrolix library passes ALL
				// tracker peers (including our own address) to every registered dialer.
				// Without this, the client spams the relay server with ~10-20 connect
				// requests/second when seeding many torrents (one per torrent per poll cycle).
				localPort := torrentClient.GetUnderlyingClient().LocalPort()
				if pubIP := dcp.GetPublicIP(); pubIP != "" {
					selfAddr := fmt.Sprintf("%s:%d", pubIP, localPort)
					relayDialer.AddOwnAddr(selfAddr)
					log.Printf("[relay] Relay dialer: registered own address %s (will never relay-dial self)", selfAddr)
				}
				// Also register loopback and any local IPs on this port
				for _, localAddr := range getLocalAddresses(localPort) {
					relayDialer.AddOwnAddr(localAddr)
				}

				torrentClient.GetUnderlyingClient().AddDialer(relayDialer)
				log.Printf("[relay] Relay dialer added to torrent client")

				// Detect if we are behind NAT
				natDetector := relay.NewNATDetector(cfg.MainServerURL, remoteID.String(), localPort)

				// Run NAT detection (blocks for first check, then periodic in background)
				natStatus := natDetector.DetectOnce()
				natDetector.StartPeriodicCheck(10*time.Minute, ctx.Done())

				if natStatus.IsBehindNAT {
					// We're behind NAT — start relay client to register with the relay server
					// so other peers can reach us through the relay.
					advertisedAddr := fmt.Sprintf("%s:%d", natStatus.ExternalIP, localPort)
					if natStatus.ExternalIP == "" {
						// Fallback: use the IP from main server URL
						if pubIP := dcp.GetPublicIP(); pubIP != "" {
							advertisedAddr = fmt.Sprintf("%s:%d", pubIP, localPort)
						}
					}

					relayClient := relay.NewClient(relayAddr, advertisedAddr)

					// Register our NAT external address as a self address so the relay
					// dialer never tries to dial ourselves through the relay.
					relayDialer.AddOwnAddr(advertisedAddr)

					// Add relay listener so the torrent client accepts incoming connections
					// that arrive through the relay (for when WE are the seeder).
					relayListener := relay.NewRelayListener(relayClient, relayAddr)
					torrentClient.GetUnderlyingClient().AddListener(relayListener)
					log.Printf("[relay] Relay listener added for incoming relay connections")

					go relayClient.Start(ctx)
					log.Printf("[relay] NAT detected — relay client started, registered as %s via relay %s", advertisedAddr, relayAddr)
				} else {
					log.Printf("[relay] Server is directly reachable — relay client not needed (dialer still active for connecting to NATted peers)")
				}
			}

			// Initialize WebSocket client connector for bidirectional communication with main server
			wsClient, err = ws.NewClientConnector(
				database.DB,
				remoteID,
				cfg.ServerName,
				cfg.ServerLocation,
				cfg.MainServerURL,
				cfg.RegistrationKey,
				Version,
				cfg.ScanPath,
			)
			if err != nil {
				log.Printf("Warning: failed to create WebSocket client: %v", err)
			} else {
				// Set command handlers
				wsClient.SetOnRestart(func() error {
					log.Println("[WS Client] Restart command received")
					return nil
				})

				wsClient.SetOnUpgrade(func(version string) error {
					log.Printf("[WS Client] Upgrade command received: version %s", version)
					// Trigger the upgrade process
					// This will be handled by the updater agent
					return nil
				})

				wsClient.SetOnRescan(func() error {
					log.Println("[WS Client] Rescan command received")
					go periodicScanner.RunFullScan()
					return nil
				})

				wsClient.SetOnStatusRequest(func() map[string]interface{} {
					packageCount, _ := database.CountDCPPackages()
					return map[string]interface{}{
						"packages": packageCount,
						"version":  Version,
					}
				})

				wsClient.SetOnDeleteContent(func(packageID, packageName, infoHash, targetPath string) (string, string, error) {
					log.Printf("[WS Client] Delete content command: package=%s name=%s hash=%s path=%s", packageID, packageName, infoHash, targetPath)
					result, message := transferProcessor.DeleteContent(packageID, packageName, infoHash, targetPath)
					if result == "error" {
						return result, message, fmt.Errorf("%s", message)
					}
					return result, message, nil
				})

				// Start WebSocket client
				go wsClient.Start(ctx)
				log.Println("WebSocket client connector started")

				// Start activity reporter — sends live activity data to main server via WebSocket
				activityReporter := ws.NewActivityReporter(wsClient, 5*time.Second)

				// Register torrent activity collector
				activityReporter.RegisterCollector("torrents", func() []ws.ActivityItem {
					stats := torrentClient.GetActivityStats()
					var items []ws.ActivityItem
					seedingCount := 0
					for _, stat := range stats {
						if stat.IsErrored {
							items = append(items, ws.ActivityItem{
								Category: "downloading",
								Action:   "error",
								Title:    fmt.Sprintf("Download error: %s", stat.PackageName),
								Detail:   stat.ErrorMessage,
								Extra:    map[string]interface{}{"info_hash": stat.InfoHash},
							})
							continue
						}
						if stat.HasTransfer && stat.Progress < 100 {
							items = append(items, ws.ActivityItem{
								Category: "downloading",
								Action:   "progress",
								Title:    fmt.Sprintf("Downloading: %s", stat.PackageName),
								Detail:   fmt.Sprintf("%s / %s (%d peers)", ws.FormatBytes(stat.BytesCompleted), ws.FormatBytes(stat.BytesTotal), stat.PeersConnected),
								Progress: stat.Progress,
								Extra: map[string]interface{}{
									"info_hash":       stat.InfoHash,
									"peers_connected": stat.PeersConnected,
									"bytes_completed": stat.BytesCompleted,
									"bytes_total":     stat.BytesTotal,
								},
							})
						} else if stat.IsSeeding {
							seedingCount++
						}
					}
					if seedingCount > 0 {
						items = append(items, ws.ActivityItem{
							Category: "seeding",
							Action:   "progress",
							Title:    fmt.Sprintf("Seeding %d torrent(s)", seedingCount),
							Extra:    map[string]interface{}{"count": seedingCount},
						})
					}
					return items
				})

				// Register queue activity collector
				activityReporter.RegisterCollector("queue", ws.NewQueueActivityCollector(database.DB, remoteID.String()))

				// Register scanner activity collector
				activityReporter.RegisterCollector("scanner", func() []ws.ActivityItem {
					isScanning, scanStarted, _, packagesFound := periodicScanner.GetScanState()
					if !isScanning {
						return nil
					}
					item := ws.ActivityItem{
						Category:  "scanner",
						Action:    "progress",
						Title:     "Scanning DCP library",
						StartedAt: &scanStarted,
					}
					if packagesFound > 0 {
						item.Extra = map[string]interface{}{"packages_found": packagesFound}
					}
					return []ws.ActivityItem{item}
				})

				// Register pending transfers collector
				activityReporter.RegisterCollector("transfers", ws.NewTransferPendingCollector(database.DB, remoteID.String()))

				go activityReporter.Start(ctx)
				log.Println("Activity reporter started")
			}
		}
	} else if cfg.IsMainServer() {
		// Main server also needs to download DCPs via torrent when transfers are requested for it.
		// We point the TransferProcessor at our own local API (localhost) since we serve those endpoints.
		mainServerLocalURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort)

		// Get the main server's MAC address for auth headers
		mainMAC, macErr := getMACAddress()
		if macErr != nil {
			log.Printf("Warning: could not get MAC address for main server transfer processor: %v", macErr)
			mainMAC = "unknown"
		}

		// Determine download directory: use torrent_download_location from DB if set, else scan path
		mainTorrentDownloadDir := cfg.ScanPath
		var dbTorrentDL string
		dbErr := database.DB.QueryRow(
			`SELECT COALESCE(torrent_download_location, '') FROM servers WHERE id = $1`, serverID,
		).Scan(&dbTorrentDL)
		if dbErr == nil && dbTorrentDL != "" {
			mainTorrentDownloadDir = dbTorrentDL
			log.Printf("[main-server] Using torrent download location from DB: %s", mainTorrentDownloadDir)
		} else {
			log.Printf("[main-server] Using config scan path for torrent downloads: %s", mainTorrentDownloadDir)
		}
		if mkErr := os.MkdirAll(mainTorrentDownloadDir, 0755); mkErr != nil {
			log.Printf("[main-server] Warning: could not create torrent download dir %s: %v", mainTorrentDownloadDir, mkErr)
		}
		torrentClient.SetDownloadPath(mainTorrentDownloadDir)

		// Start transfer processor: polls own API for transfers destined for this (main) server
		mainTransferProcessor := torrentpkg.NewTransferProcessor(
			torrentClient,
			mainServerLocalURL,
			serverID.String(),
			mainMAC,
			mainTorrentDownloadDir,
		)
		torrentClient.SetErrorReporter(func(transferID, status, errorMessage string) error {
			return mainTransferProcessor.ReportTransferError(transferID, status, errorMessage)
		})
		go mainTransferProcessor.Start(ctx)
		defer mainTransferProcessor.Stop()
		log.Printf("[main-server] Transfer processor started (polling %s for transfers)", mainServerLocalURL)

		// Resume any in-progress downloads from before restart
		go torrentClient.ResumeDownloads()

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

	// Start periodic scanner NOW — after settings client is configured (for clients)
	// so the initial scan fetches ALL library locations from the main server.
	periodicScanner.Start()
	log.Println("Periodic scanner started with all configured library locations")

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

// runMigrations executes database migrations compiled into the binary.
// All SQL is stored in migrations.go as Go string constants so migrations
// work on deployed servers without needing the source tree on disk.
func runMigrations(database *db.DB) error {
	log.Println("Running database migrations (embedded in binary)...")

	for _, name := range migrationOrder {
		sql, ok := embeddedMigrationSQL[name]
		if !ok {
			log.Printf("  Warning: migration %q not found in embedded SQL, skipping", name)
			continue
		}

		log.Printf("  Running migration: %s", name)

		if _, err := database.Exec(sql); err != nil {
			log.Printf("  Warning: %s: %v", name, err)
			// Continue with other migrations - they use IF NOT EXISTS / IF NOT EXISTS
		} else {
			log.Printf("  ✓ %s completed", name)
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

// getLocalAddresses returns all local IP:port strings for the given port.
// Used to register own addresses with the relay dialer so it never dials itself.
func getLocalAddresses(port int) []string {
	var addrs []string
	addrs = append(addrs, fmt.Sprintf("127.0.0.1:%d", port))
	addrs = append(addrs, fmt.Sprintf("::1:%d", port))

	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}
	for _, iface := range ifaces {
		ifAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifAddrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && !ip.IsLoopback() {
				addrs = append(addrs, fmt.Sprintf("%s:%d", ip.String(), port))
			}
		}
	}
	return addrs
}

// deriveRelayAddr extracts the host from mainServerURL and combines it with the relay port.
// e.g. "http://1.2.3.4:10858" + port 10866 → "1.2.3.4:10866"
func deriveRelayAddr(mainServerURL string, relayPort int) string {
	// Strip protocol prefix
	host := mainServerURL
	if idx := strings.Index(host, "://"); idx >= 0 {
		host = host[idx+3:]
	}
	// Strip path suffix
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	// Strip existing port
	if idx := strings.LastIndex(host, ":"); idx >= 0 {
		host = host[:idx]
	}
	return fmt.Sprintf("%s:%d", host, relayPort)
}
