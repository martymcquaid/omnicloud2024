package torrent

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/google/uuid"
)

// speedSample tracks cumulative byte counters for speed calculation
type speedSample struct {
	bytesRead    int64
	bytesWritten int64
	timestamp    time.Time
}

// TransferErrorReporter is a callback to report transfer errors to the main server.
// On the main server, this updates the local DB. On clients, this calls the main server API.
type TransferErrorReporter func(transferID, status, errorMessage string) error

// Client manages torrent seeding and downloading
type Client struct {
	client             *torrent.Client
	db                 *sql.DB
	serverID           string
	completionDir      string   // base dir for per-torrent BoltDB piece completion caches
	scanPath           string   // DCP library path for download destination
	trackerPort        int      // local tracker port; when set, announces go to 127.0.0.1:<port> so the server can reach its own tracker
	trackerAnnounceURL string   // full tracker announce URL (e.g. "http://dcp1.example.com:10851/announce"); used to fix port-0 URLs in old .torrent files
	tracker            *Tracker // in-process tracker for direct seeder registration (nil on client servers)

	// Track active torrents
	mu       sync.RWMutex
	torrents map[string]*ActiveTorrent // key: info_hash

	// Speed tracking
	speedMu      sync.Mutex
	speedSamples map[string]speedSample // key: info_hash

	// Error reporting callback (set by TransferProcessor on client mode)
	errorReporter TransferErrorReporter
}

// ActiveTorrent represents a torrent being seeded or downloaded
type ActiveTorrent struct {
	Torrent       *torrent.Torrent
	InfoHash      string
	PackageID     string
	LocalPath     string
	TransferID    string // non-empty for downloads; used for resume
	IsSeeding     bool
	IsDownloading bool
	IsErrored     bool   // true when download has a persistent error
	ErrorMessage  string // human-readable error message
	AddedAt       time.Time
	SeederPeerID  string // stable peer ID for tracker registration (prevents duplicates)
	AnnounceURL   string // original announce URL from the torrent file (preserved for re-verify)
	TorrentBytes  []byte // raw .torrent file bytes for re-adding after path switch

	// Write error tracking for download error detection
	writeErrCount int32     // atomic counter for consecutive write errors
	lastWriteErr  time.Time // timestamp of last write error

	// Integrity watcher: set to true after we've detected deletion and triggered re-verify.
	// Prevents the watcher from repeatedly dropping+re-adding every 30s while files are still missing.
	integrityReset bool
}

// TorrentStats contains statistics for a torrent
type TorrentStats struct {
	InfoHash        string
	BytesCompleted  int64
	BytesTotal      int64
	DownloadSpeed   int64 // bytes/sec
	UploadSpeed     int64 // bytes/sec
	PeersConnected  int
	PeersTotal      int
	Progress        float64
	PiecesCompleted int
	PiecesTotal     int
	IsSeeding       bool
	IsDownloading   bool
	IsErrored       bool
	ErrorMessage    string
	HasTransfer     bool // true if this torrent is associated with a transfer (download, not seed)
	ETA             int  // seconds

	// Raw cumulative bytes from the library — used by the reporter to compute
	// speed independently of the download monitor's speedSamples.
	rawBytesRead    int64
	rawBytesWritten int64
}

// NewClient creates a new torrent client.
// completionDir is the base directory for piece completion caches.
// scanPath is the DCP library path used as download destination for transfers.
// trackerAnnounceURL is the full external tracker URL (e.g. "http://dcp1.example.com:10851/announce").
// It is used to fix .torrent files that were generated with a port-0 announce URL.
func NewClient(cfg *torrent.ClientConfig, db *sql.DB, serverID, completionDir, scanPath, trackerAnnounceURL string, trackerPort int) (*Client, error) {
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	// Ensure completion dir exists
	if completionDir != "" {
		os.MkdirAll(completionDir, 0755)
	}

	return &Client{
		client:             cl,
		db:                 db,
		serverID:           serverID,
		completionDir:      completionDir,
		scanPath:           scanPath,
		trackerPort:        trackerPort,
		trackerAnnounceURL: trackerAnnounceURL,
		torrents:           make(map[string]*ActiveTorrent),
		speedSamples:       make(map[string]speedSample),
	}, nil
}

// SetDownloadPath updates the download destination path (for fetching from main server settings)
// GetUnderlyingClient returns the anacrolix/torrent Client for relay integration.
// Used to add custom dialers and listeners for NAT traversal.
func (c *Client) GetUnderlyingClient() *torrent.Client {
	return c.client
}

func (c *Client) SetDownloadPath(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scanPath = path
	log.Printf("[torrent-client] Download path updated to: %s", path)
}

// Close closes the torrent client and all active torrents
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Printf("[SEED-HEALTH] Client.Close() called — dropping %d active torrents", len(c.torrents))

	// Close all torrents
	for hash, at := range c.torrents {
		infoName := ""
		if at.Torrent.Info() != nil {
			infoName = at.Torrent.Info().Name
		}
		log.Printf("[SEED-HEALTH] Dropping torrent %s (%s)", hash[:12], infoName)
		at.Torrent.Drop()
	}

	// Close client
	c.client.Close()
	return nil
}

// SetTracker sets the in-process tracker for direct seeder registration.
func (c *Client) SetTracker(tracker *Tracker) {
	c.tracker = tracker
}

// SetErrorReporter sets a callback for reporting transfer errors to the main server.
func (c *Client) SetErrorReporter(reporter TransferErrorReporter) {
	c.errorReporter = reporter
}

// localAnnounceURL returns the announce URL for the local torrent client to use.
// If trackerPort is set, it rewrites any announce URL to 127.0.0.1:<trackerPort>
// so the server can reach its own tracker (the public IP may not be routable from localhost).
// For client servers (trackerPort == 0), if the announce URL has port 0 (from old .torrent files
// generated before tracker_port was properly configured), fall back to trackerAnnounceURL.
func (c *Client) localAnnounceURL(announce string) string {
	if c.trackerPort > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/announce", c.trackerPort)
	}
	// Fix port-0 announce URLs baked into old .torrent files (e.g. "http://host:0/announce")
	if c.trackerAnnounceURL != "" && strings.Contains(announce, ":0/") {
		log.Printf("[torrent-client] Fixing port-0 announce URL %q → %q", announce, c.trackerAnnounceURL)
		return c.trackerAnnounceURL
	}
	return announce
}

// generatePeerID creates a stable peer ID for a given info hash.
// Uses the server ID + info hash prefix so the same torrent always gets the same peer ID.
func (c *Client) generatePeerID(infoHash string) string {
	// Peer IDs are 20 bytes in BitTorrent. Use a deterministic ID so re-registration
	// updates the existing peer entry instead of creating duplicates.
	raw := fmt.Sprintf("-OC0001-%s%s", c.serverID[:8], infoHash[:4])
	if len(raw) > 20 {
		raw = raw[:20]
	}
	return raw
}

// announceToTracker registers this server with the in-process tracker.
// Uses direct Go method call (no HTTP) to avoid URL encoding issues with binary info hashes.
// peerID must be stable across calls to prevent duplicate peer entries in the tracker.
// bytesLeft is how many bytes remain to download (0 = complete seeder).
func (c *Client) announceToTracker(infoHash metainfo.Hash, peerID string, bytesLeft int64) {
	if c.tracker == nil {
		log.Printf("announceToTracker %s: no tracker reference, skipping", infoHash.HexString()[:12])
		return
	}

	port := c.client.LocalPort()

	// Use the tracker's announce host (public IP) so peers see a reachable address
	ip := c.tracker.announceHost
	if ip == "" {
		ip = "127.0.0.1"
	}

	log.Printf("[announce] Registering seeder: hash=%s peerID=%s ip=%s port=%d left=%d",
		infoHash.HexString()[:12], peerID[:12], ip, port, bytesLeft)
	c.tracker.RegisterSeeder(infoHash.HexString(), peerID, ip, port, bytesLeft)
}

// StartSeeding starts seeding a DCP from a torrent file
func (c *Client) StartSeeding(torrentBytes []byte, dataPath, packageID, torrentID string) error {
	// Parse torrent metainfo
	var mi metainfo.MetaInfo
	err := bencode.Unmarshal(torrentBytes, &mi)
	if err != nil {
		return fmt.Errorf("failed to parse torrent: %w", err)
	}

	infoHash := mi.HashInfoBytes().HexString()

	// Check if already seeding
	c.mu.RLock()
	if _, exists := c.torrents[infoHash]; exists {
		c.mu.RUnlock()
		return nil // Already seeding
	}
	c.mu.RUnlock()

	// Use per-torrent storage pointing to the actual DCP parent directory
	// so the torrent client can find the data files on disk.
	// dataPath is e.g. /APPBOX_DATA/storage/DCP/TESTLIBRARY/PackageName
	// The torrent info.Name matches the directory name, so parent dir is the base.
	parentDir := filepath.Dir(dataPath)

	// Use PostgreSQL for piece completion tracking instead of BoltDB.
	// This avoids file locking issues when starting many torrents simultaneously,
	// AND persists verification results across restarts (huge startup speedup for large DCPs).
	completion := NewPostgresPieceCompletion(c.db, mi.HashInfoBytes())
	torrentStorage := storage.NewFileWithCompletion(parentDir, completion)

	// Use localhost announce URL so the server can reach its own tracker
	announceURL := c.localAnnounceURL(mi.Announce)
	log.Printf("StartSeeding %s: announce=%s (original=%s)", infoHash, announceURL, mi.Announce)

	// Add torrent (must set InfoHash so the library accepts the info bytes)
	t, _, err := c.client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  mi.HashInfoBytes(),
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{announceURL}},
		Storage:   torrentStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	// Set a custom write error handler for seeding torrents too.
	// Without this, the default handler permanently disables data upload/download
	// on the first disk error, silently killing the torrent.
	t.SetOnWriteChunkError(func(err error) {
		log.Printf("[SEED-HEALTH] WRITE CHUNK ERROR for %s: %v — torrent may stop working", infoHash[:12], err)
		// Re-allow download so the library doesn't permanently disable the torrent
		time.Sleep(2 * time.Second)
		t.AllowDataDownload()
		log.Printf("[SEED-HEALTH] Re-enabled data download for %s after write error", infoHash[:12])
	})

	// Wait for torrent info (should be immediate since we have .torrent file)
	<-t.GotInfo()
	log.Printf("StartSeeding %s: info received, bytesCompleted=%d/%d seeding=%v",
		infoHash, t.BytesCompleted(), t.Length(), t.Seeding())

	// Generate a stable peer ID for tracker registration
	stablePeerID := c.generatePeerID(infoHash)

	// Store active torrent
	c.mu.Lock()
	c.torrents[infoHash] = &ActiveTorrent{
		Torrent:      t,
		InfoHash:     infoHash,
		PackageID:    packageID,
		LocalPath:    dataPath,
		IsSeeding:    true,
		AddedAt:      time.Now(),
		SeederPeerID: stablePeerID,
		TorrentBytes: torrentBytes,
	}
	c.mu.Unlock()

	// Register with in-process tracker so it knows we're seeding.
	// The anacrolix library's built-in announcer uses the public IP URL which
	// may not be reachable from localhost, so we register directly.
	// Report actual bytes remaining so incomplete torrents aren't falsely advertised as seeders.
	if c.tracker != nil {
		bytesLeft := t.Length() - t.BytesCompleted()
		if bytesLeft < 0 {
			bytesLeft = 0
		}
		c.announceToTracker(mi.HashInfoBytes(), stablePeerID, bytesLeft)
	}

	// Register as seeder in database
	return c.registerSeeder(torrentID, dataPath, "seeding")
}

// SwitchSeedingPath drops a seeding torrent and re-adds it with a new data path.
// Used when RosettaBridge ingests a DCP and the files move to a new location.
// Piece completion data in PostgreSQL is keyed by (info_hash, piece_index), not path,
// so it remains valid after the switch.
func (c *Client) SwitchSeedingPath(infoHash, newDataPath, torrentID string) error {
	c.mu.Lock()
	at, exists := c.torrents[infoHash]
	if !exists {
		c.mu.Unlock()
		return fmt.Errorf("torrent %s not found", infoHash)
	}

	oldPath := at.LocalPath
	torrentBytes := at.TorrentBytes
	packageID := at.PackageID

	if len(torrentBytes) == 0 {
		c.mu.Unlock()
		return fmt.Errorf("torrent %s has no stored torrent bytes, cannot re-add", infoHash)
	}

	// Drop the current torrent
	at.Torrent.Drop()
	delete(c.torrents, infoHash)
	c.mu.Unlock()

	log.Printf("[ingestion] Switching seeding for %s from %s to %s", infoHash[:12], oldPath, newDataPath)

	// Re-add with the new path
	err := c.StartSeeding(torrentBytes, newDataPath, packageID, torrentID)
	if err != nil {
		return fmt.Errorf("failed to re-add torrent at new path: %w", err)
	}

	log.Printf("[ingestion] Successfully switched seeding for %s to %s", infoHash[:12], newDataPath)
	return nil
}

// StartSeedingWithSplitPath seeds a canonical torrent from two directories:
//   - mxfPath: the library directory containing the large MXF content files
//   - xmlShadowPath: a shadow directory containing the canonical XML metadata files
//     (ASSETMAP.xml, PKL.xml, etc.) downloaded from the main server
//
// This is used for cross-site co-seeding when RosettaBridge has delivered the same DCP to
// multiple sites with different ASSETMAP UUIDs. The MXF files are identical on both sites,
// but the XML files differ. The canonical XML files are stored in the shadow directory so
// that RosettaBridge's original library files are not disturbed.
func (c *Client) StartSeedingWithSplitPath(torrentBytes []byte, mxfPath, xmlShadowPath, packageID, torrentID string) error {
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentBytes, &mi); err != nil {
		return fmt.Errorf("failed to parse torrent: %w", err)
	}

	infoHash := mi.HashInfoBytes().HexString()

	// Check if already seeding this torrent
	c.mu.RLock()
	if _, exists := c.torrents[infoHash]; exists {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	// mxfPath is e.g. /library/IZ469BACK_ADV_.../
	// The torrent's info.Name is the DCP dir name, so we need its PARENT for standard storage.
	// For split storage, we pass the parents of both paths and let the storage append info.Name.
	mxfParentDir := filepath.Dir(mxfPath)
	// xmlShadowPath is already the package-level shadow dir (e.g. /canonical-xml/<pkg_id>/)
	// The torrent's info.Name will be appended by the storage. We need to ensure the shadow
	// dir contains a subdirectory named after info.Name. We use the shadow path's parent.
	xmlParentDir := filepath.Dir(xmlShadowPath)

	completion := NewPostgresPieceCompletion(c.db, mi.HashInfoBytes())
	splitStorage := NewSplitPathStorage(mxfParentDir, xmlParentDir, completion)

	announceURL := c.localAnnounceURL(mi.Announce)
	log.Printf("[split-seed] Starting split-path seeding: infoHash=%s mxfDir=%s xmlDir=%s",
		infoHash, mxfPath, xmlShadowPath)

	t, _, err := c.client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  mi.HashInfoBytes(),
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{announceURL}},
		Storage:   splitStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to add split-path torrent: %w", err)
	}

	t.SetOnWriteChunkError(func(err error) {
		log.Printf("[split-seed] Write error for %s: %v", infoHash[:12], err)
		time.Sleep(2 * time.Second)
		t.AllowDataDownload()
	})

	<-t.GotInfo()
	log.Printf("[split-seed] Info received for %s: bytesCompleted=%d/%d",
		infoHash, t.BytesCompleted(), t.Length())

	stablePeerID := c.generatePeerID(infoHash)

	c.mu.Lock()
	c.torrents[infoHash] = &ActiveTorrent{
		Torrent:      t,
		InfoHash:     infoHash,
		PackageID:    packageID,
		LocalPath:    mxfPath,
		IsSeeding:    true,
		AddedAt:      time.Now(),
		SeederPeerID: stablePeerID,
		TorrentBytes: torrentBytes,
	}
	c.mu.Unlock()

	if c.tracker != nil {
		bytesLeft := t.Length() - t.BytesCompleted()
		if bytesLeft < 0 {
			bytesLeft = 0
		}
		c.announceToTracker(mi.HashInfoBytes(), stablePeerID, bytesLeft)
	}

	return c.registerSeeder(torrentID, mxfPath, "seeding")
}

// StartDownload starts downloading a DCP via torrent
func (c *Client) StartDownload(torrentBytes []byte, destPath, packageID, transferID string) error {
	log.Printf("[download] StartDownload called: packageID=%s transferID=%s dest=%s torrentBytes=%d",
		packageID, transferID, destPath, len(torrentBytes))

	// Parse torrent metainfo
	var mi metainfo.MetaInfo
	err := bencode.Unmarshal(torrentBytes, &mi)
	if err != nil {
		return fmt.Errorf("failed to parse torrent (%d bytes): %w", len(torrentBytes), err)
	}

	infoHash := mi.HashInfoBytes().HexString()
	log.Printf("[download] Parsed torrent: infoHash=%s announce=%s", infoHash, mi.Announce)

	// Log all announce URLs found in the torrent
	for i, tier := range mi.AnnounceList {
		for j, url := range tier {
			log.Printf("[download] %s: announceList[%d][%d]=%s", infoHash[:12], i, j, url)
		}
	}

	// Check if already downloading/seeding
	c.mu.RLock()
	if existing, exists := c.torrents[infoHash]; exists {
		c.mu.RUnlock()
		log.Printf("[download] Torrent %s already active (downloading=%v seeding=%v), skipping",
			infoHash, existing.IsDownloading, existing.IsSeeding)
		return nil // Already active
	}
	c.mu.RUnlock()

	// Ensure destination directory exists
	// destPath is e.g. /library/omnicloud/OmniCloudAPPLibrary/PackageName
	// parentDir is e.g. /library/omnicloud/OmniCloudAPPLibrary/
	parentDir := filepath.Dir(destPath)
	log.Printf("[download] Creating parent directory: %s", parentDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory %s: %w", parentDir, err)
	}
	// Verify the directory is writable before starting the download
	testFile := filepath.Join(parentDir, ".omnicloud-write-test")
	if err := ioutil.WriteFile(testFile, []byte("test"), 0644); err != nil {
		return fmt.Errorf("download directory %s is not writable (read-only filesystem?): %w", parentDir, err)
	}
	os.Remove(testFile)

	// Use PostgreSQL for piece completion tracking (same as StartSeeding).
	// This persists download progress across restarts so pieces don't need re-downloading.
	completion := NewPostgresPieceCompletion(c.db, mi.HashInfoBytes())
	torrentStorage := storage.NewFileWithCompletion(parentDir, completion)
	log.Printf("[download] PostgresPieceCompletion and storage configured for %s", infoHash)

	// Use the announce URL as-is for clients — they need to reach the remote tracker.
	// localAnnounceURL only rewrites to 127.0.0.1 on the main server (where trackerPort > 0).
	announceURL := c.localAnnounceURL(mi.Announce)
	log.Printf("[download] %s: announce=%s (original=%s) trackerPort=%d dest=%s",
		infoHash, announceURL, mi.Announce, c.trackerPort, destPath)

	// Add torrent with custom storage pointing to download destination
	log.Printf("[download] Adding torrent spec to client...")
	t, _, err := c.client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  mi.HashInfoBytes(),
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{announceURL}},
		Storage:   torrentStorage,
	})
	if err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	// Log the local listening port so we know what port this client is using
	localPort := c.client.LocalPort()
	log.Printf("[download] Torrent added to client. Local listen port: %d", localPort)

	// Wait for torrent info (should be immediate since we provided InfoBytes)
	<-t.GotInfo()

	log.Printf("[download] %s: info received, name=%q files=%d bytesCompleted=%d/%d peers=%d",
		infoHash, t.Info().Name, len(t.Info().Files), t.BytesCompleted(), t.Length(), len(t.PeerConns()))

	// Log piece info
	log.Printf("[download] %s: pieceLength=%d numPieces=%d totalLength=%d",
		infoHash[:12], t.Info().PieceLength, t.Info().NumPieces(), t.Info().TotalLength())

	// Pre-create all subdirectories needed by the torrent files.
	// The anacrolix storage layer does NOT create parent directories for individual files,
	// so we must create them here or every write will fail with "no such file or directory".
	for _, f := range t.Info().Files {
		if len(f.Path) > 1 {
			// Multi-level path, e.g. ["PackageName", "subdir", "file.mxf"]
			subdir := filepath.Join(parentDir, filepath.Join(f.Path[:len(f.Path)-1]...))
			if err := os.MkdirAll(subdir, 0755); err != nil {
				log.Printf("[download] WARNING: failed to create subdir %s: %v", subdir, err)
			}
		} else if len(f.Path) == 1 {
			// Single-level path, e.g. ["PackageName/file.mxf"] — ensure parent exists
			fullPath := filepath.Join(parentDir, f.Path[0])
			dir := filepath.Dir(fullPath)
			if dir != parentDir {
				if err := os.MkdirAll(dir, 0755); err != nil {
					log.Printf("[download] WARNING: failed to create dir %s: %v", dir, err)
				}
			}
		}
	}
	// Also ensure the torrent name directory exists (the top-level DCP folder)
	dcpDir := filepath.Join(parentDir, t.Info().Name)
	if err := os.MkdirAll(dcpDir, 0755); err != nil {
		log.Printf("[download] WARNING: failed to create DCP directory %s: %v", dcpDir, err)
	}
	log.Printf("[download] %s: pre-created download directories under %s", infoHash[:12], parentDir)

	// Store active torrent BEFORE setting up the error handler (handler references at)
	at := &ActiveTorrent{
		Torrent:       t,
		InfoHash:      infoHash,
		PackageID:     packageID,
		LocalPath:     destPath,
		TransferID:    transferID,
		IsDownloading: true,
		AddedAt:       time.Now(),
		AnnounceURL:   announceURL, // Preserve for re-verify after file deletion
		TorrentBytes:  torrentBytes,
	}
	c.mu.Lock()
	c.torrents[infoHash] = at
	c.mu.Unlock()

	// Set a custom write error handler that detects persistent disk errors.
	// The default handler silently disables all downloading on first write error.
	// Our handler:
	// 1. Tries to create missing directories (fixes "no such file or directory")
	// 2. On persistent errors (50+ in 30s), marks the transfer as errored and stops
	// 3. Reports the error to the main server for display in the UI
	const writeErrorThreshold = 50 // errors within the window to trigger failure
	t.SetOnWriteChunkError(func(writeErr error) {
		errMsg := writeErr.Error()
		count := atomic.AddInt32(&at.writeErrCount, 1)

		// Rate-limit logging to avoid flood (log every 50th error)
		if count <= 3 || count%50 == 0 {
			log.Printf("[download] WRITE CHUNK ERROR #%d for %s: %v", count, infoHash[:12], writeErr)
		}

		// Try to fix "no such file or directory" by creating the directory
		if strings.Contains(errMsg, "no such file or directory") {
			// Extract the file path from the error message
			// Error format: "open /path/to/file.mxf: no such file or directory"
			parts := strings.SplitN(errMsg, ": ", 2)
			if len(parts) >= 1 {
				filePath := strings.TrimPrefix(parts[0], "open ")
				dir := filepath.Dir(filePath)
				if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr == nil {
					log.Printf("[download] %s: created missing directory %s", infoHash[:12], dir)
					t.AllowDataDownload()
					// Reset error count since we fixed the problem
					atomic.StoreInt32(&at.writeErrCount, 0)
					return
				}
			}
		}

		// Check for persistent/fatal errors (read-only filesystem, permission denied, disk full)
		isFatal := strings.Contains(errMsg, "read-only file system") ||
			strings.Contains(errMsg, "permission denied") ||
			strings.Contains(errMsg, "no space left on device")

		if isFatal || count >= int32(writeErrorThreshold) {
			// Mark this download as errored — stop retrying
			c.mu.Lock()
			if !at.IsErrored {
				at.IsErrored = true
				at.IsDownloading = false
				// Create a clean error message for the UI
				if strings.Contains(errMsg, "read-only file system") {
					at.ErrorMessage = "Disk is read-only — cannot write downloaded data"
				} else if strings.Contains(errMsg, "permission denied") {
					at.ErrorMessage = "Permission denied — cannot write to download directory"
				} else if strings.Contains(errMsg, "no space left on device") {
					at.ErrorMessage = "Disk full — no space left on device"
				} else if strings.Contains(errMsg, "no such file or directory") {
					at.ErrorMessage = "Download directory does not exist or cannot be created"
				} else {
					at.ErrorMessage = fmt.Sprintf("Disk write error: %s", errMsg)
				}
				log.Printf("[download] %s: PERSISTENT WRITE ERROR — marking transfer as errored: %s",
					infoHash[:12], at.ErrorMessage)

				// Report error to the database / main server
				c.updateTransferError(transferID, at.ErrorMessage)
			}
			c.mu.Unlock()
			return // Don't re-allow download — let the monitor notice and stop
		}

		// Transient error — re-allow download after a brief pause
		time.Sleep(2 * time.Second)
		t.AllowDataDownload()
	})

	// Download all files
	t.DownloadAll()

	log.Printf("[download] %s: download started successfully, launching monitor goroutine", infoHash)

	// Start monitoring download in background
	go c.monitorDownload(infoHash, transferID)

	return nil
}

// monitorDownload monitors download progress and updates transfer status
func (c *Client) monitorDownload(infoHash, transferID string) {
	log.Printf("[download-monitor] Started monitoring %s (transfer=%s)", infoHash[:12], transferID)

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	logCounter := 0
	var lastBytesCompleted int64
	stuckCount := 0 // how many consecutive ticks with zero progress

	for range ticker.C {
		c.mu.RLock()
		at, exists := c.torrents[infoHash]
		c.mu.RUnlock()

		if !exists {
			log.Printf("[download-monitor] Torrent %s removed from active list, stopping monitor", infoHash[:12])
			return // Torrent removed
		}

		// Check if the download has been marked as errored by the write error handler
		if at.IsErrored {
			log.Printf("[download-monitor] %s: download has PERSISTENT DISK ERROR: %s — stopping monitor",
				infoHash[:12], at.ErrorMessage)
			// Drop the torrent from the library to stop all I/O
			at.Torrent.Drop()
			return
		}

		t := at.Torrent
		stats := c.getTorrentStats(t)
		logCounter++

		// Detailed diagnostics from the library's internal stats
		tStats := t.Stats()
		numPeerConns := len(t.PeerConns())
		numKnownPeers := len(t.KnownSwarm())

		log.Printf("[download-monitor] %s: progress=%.1f%% (%d/%d bytes) down=%d B/s up=%d B/s",
			infoHash[:12], stats.Progress, stats.BytesCompleted, stats.BytesTotal,
			stats.DownloadSpeed, stats.UploadSpeed)

		log.Printf("[download-monitor] %s: peers: activeConns=%d knownSwarm=%d totalPeers=%d halfOpen=%d "+
			"pending=%d connectedSeeders=%d chunksRead=%d chunksWritten=%d",
			infoHash[:12],
			numPeerConns,
			numKnownPeers,
			tStats.TotalPeers,
			tStats.HalfOpenPeers,
			tStats.PendingPeers,
			tStats.ConnectedSeeders,
			tStats.ChunksRead.Int64(),
			tStats.ChunksWritten.Int64())

		// Track stuck downloads
		if stats.BytesCompleted == lastBytesCompleted && stats.BytesCompleted < stats.BytesTotal {
			stuckCount++
		} else {
			stuckCount = 0
		}
		lastBytesCompleted = stats.BytesCompleted

		// Log active peer connection count
		peerConns := t.PeerConns()
		if len(peerConns) > 0 {
			log.Printf("[download-monitor] %s: %d active peer connection(s) established", infoHash[:12], len(peerConns))
		}

		// Every 30 seconds, log extended diagnostics
		if logCounter%3 == 1 {
			log.Printf("[download-monitor] %s: seeding=%v haveInfo=%v numPieces=%d bytesReadData=%d bytesWrittenData=%d localPort=%d",
				infoHash[:12],
				t.Seeding(),
				t.Info() != nil,
				t.NumPieces(),
				tStats.BytesReadData.Int64(),
				tStats.BytesWrittenData.Int64(),
				c.client.LocalPort())

			// Log known swarm peers (what the tracker returned)
			knownPeers := t.KnownSwarm()
			if len(knownPeers) > 0 {
				for i, kp := range knownPeers {
					if i >= 10 {
						log.Printf("[download-monitor] %s: ... and %d more known peers", infoHash[:12], len(knownPeers)-10)
						break
					}
					log.Printf("[download-monitor] %s: knownPeer[%d] id=%x addr=%v source=%v", infoHash[:12], i, kp.Id, kp.Addr, kp.Source)
				}
			} else {
				log.Printf("[download-monitor] %s: knownSwarm is EMPTY — tracker returned no peers", infoHash[:12])
			}

			// Log tracker announce URLs being used
			log.Printf("[download-monitor] %s: trackers being used:", infoHash[:12])
			for i, tier := range t.Metainfo().AnnounceList {
				for j, url := range tier {
					log.Printf("[download-monitor] %s:   tier[%d][%d]=%s", infoHash[:12], i, j, url)
				}
			}
			if mi := t.Metainfo().Announce; mi != "" {
				log.Printf("[download-monitor] %s:   primary announce=%s", infoHash[:12], mi)
			}

			if numPeerConns == 0 && numKnownPeers == 0 {
				log.Printf("[download-monitor] %s: *** WARNING *** zero peers and zero known swarm! "+
					"The tracker is not returning any seeders for this info_hash. "+
					"Verify main server is seeding and registered with tracker. "+
					"Check that tracker URL is reachable from this machine.",
					infoHash[:12])
			} else if numPeerConns == 0 && numKnownPeers > 0 {
				log.Printf("[download-monitor] %s: *** FIREWALL/NAT DETECTED *** "+
					"Known peers: %d, Active connections: 0. "+
					"Direct TCP connections to peers are failing. "+
					"Possible causes: "+
					"(1) seeder's torrent data port is behind firewall/NAT, "+
					"(2) seeder port changed since tracker registration (ListenPort=0 auto-picks), "+
					"(3) network connectivity issue between client and seeder. "+
					"If relay is enabled, the relay dialer will attempt connection through the relay server.",
					infoHash[:12], numKnownPeers)

				// Log each unreachable peer for diagnostics
				for i, kp := range t.KnownSwarm() {
					log.Printf("[download-monitor] %s: unreachable_peer[%d] addr=%v source=%v relay_attempt_pending=true",
						infoHash[:12], i, kp.Addr, kp.Source)
				}
			}

			if stuckCount >= 6 { // 60 seconds stuck
				log.Printf("[download-monitor] %s: *** STUCK *** no progress for %d seconds (%.1f%% complete). "+
					"activeConns=%d knownPeers=%d halfOpen=%d",
					infoHash[:12], stuckCount*10, stats.Progress,
					numPeerConns, numKnownPeers, tStats.HalfOpenPeers)
			}
		}

		// Update transfer in database
		err := c.updateTransferProgress(transferID, stats)
		if err != nil {
			log.Printf("[download-monitor] Error updating transfer progress for %s: %v", infoHash[:12], err)
		}

		// Check if completed
		if stats.Progress >= 100 {
			log.Printf("[download-monitor] DOWNLOAD COMPLETE: %s (%d bytes)", infoHash[:12], stats.BytesTotal)
			c.updateTransferStatus(transferID, "completed")

			// Convert to seeding
			c.mu.Lock()
			at.IsDownloading = false
			at.IsSeeding = true
			c.mu.Unlock()

			// Create ingestion tracking record for RosettaBridge detection
			c.createIngestionRecord(at.PackageID, infoHash, at.LocalPath)

			return
		}
	}
}

// GetStats returns statistics for a torrent by info hash
func (c *Client) GetStats(infoHash string) *TorrentStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	at, exists := c.torrents[infoHash]
	if !exists {
		return nil
	}

	return c.getTorrentStats(at.Torrent)
}

// getTorrentStats extracts statistics from a torrent including speed and ETA
func (c *Client) getTorrentStats(t *torrent.Torrent) *TorrentStats {
	info := t.Info()

	var bytesTotal int64
	var piecesTotal int
	if info != nil {
		bytesTotal = info.TotalLength()
		piecesTotal = info.NumPieces()
	}

	bytesCompleted := t.BytesCompleted()
	progress := 0.0
	if bytesTotal > 0 {
		progress = float64(bytesCompleted) / float64(bytesTotal) * 100
	}

	// Count completed pieces
	piecesCompleted := 0
	if info != nil {
		for i := 0; i < piecesTotal; i++ {
			if t.PieceState(i).Complete {
				piecesCompleted++
			}
		}
	}

	// Get connected peers count
	peersConnected := len(t.PeerConns())

	// Calculate speeds using cumulative byte deltas from the torrent library
	infoHash := t.InfoHash().HexString()
	tStats := t.Stats()
	currentRead := tStats.BytesReadData.Int64()
	currentWritten := tStats.BytesWrittenData.Int64()
	now := time.Now()

	var downloadSpeed, uploadSpeed int64

	c.speedMu.Lock()
	prev, hasPrev := c.speedSamples[infoHash]
	if hasPrev {
		elapsed := now.Sub(prev.timestamp).Seconds()
		if elapsed > 0 {
			downloadSpeed = int64(float64(currentRead-prev.bytesRead) / elapsed)
			uploadSpeed = int64(float64(currentWritten-prev.bytesWritten) / elapsed)
			if downloadSpeed < 0 {
				downloadSpeed = 0
			}
			if uploadSpeed < 0 {
				uploadSpeed = 0
			}
		}
	}
	c.speedSamples[infoHash] = speedSample{
		bytesRead:    currentRead,
		bytesWritten: currentWritten,
		timestamp:    now,
	}
	c.speedMu.Unlock()

	// Calculate ETA from speed and remaining bytes
	eta := 0
	bytesRemaining := bytesTotal - bytesCompleted
	if downloadSpeed > 0 && bytesRemaining > 0 {
		eta = int(bytesRemaining / downloadSpeed)
	}

	return &TorrentStats{
		InfoHash:        infoHash,
		BytesCompleted:  bytesCompleted,
		BytesTotal:      bytesTotal,
		DownloadSpeed:   downloadSpeed,
		UploadSpeed:     uploadSpeed,
		PeersConnected:  peersConnected,
		PeersTotal:      peersConnected,
		Progress:        progress,
		PiecesCompleted: piecesCompleted,
		PiecesTotal:     piecesTotal,
		ETA:             eta,
	}
}

// HasActiveDownloads returns true if any torrent has an active transfer (download) in progress.
// This is a lightweight check that doesn't call getTorrentStats (avoids corrupting speed samples).
func (c *Client) HasActiveDownloads() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, at := range c.torrents {
		if at.TransferID != "" && !at.IsErrored && !at.IsSeeding {
			return true
		}
	}
	return false
}

// GetAllStats returns statistics for all active torrents.
// Uses shared speedSamples — only the download monitor should call this.
func (c *Client) GetAllStats() map[string]*TorrentStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*TorrentStats)
	for hash, at := range c.torrents {
		stats := c.getTorrentStats(at.Torrent)
		stats.IsSeeding = at.IsSeeding
		stats.IsDownloading = at.IsDownloading
		stats.IsErrored = at.IsErrored
		stats.ErrorMessage = at.ErrorMessage
		stats.HasTransfer = at.TransferID != ""
		result[hash] = stats
	}

	return result
}

// GetAllStatsForReporter returns statistics for all active torrents WITHOUT
// touching the shared speedSamples. Speed and ETA fields are left at 0 — the
// reporter computes them from its own per-call tracking to avoid contention
// with the download monitor goroutine.
func (c *Client) GetAllStatsForReporter() map[string]*TorrentStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*TorrentStats)
	for hash, at := range c.torrents {
		t := at.Torrent
		info := t.Info()

		var bytesTotal int64
		var piecesTotal int
		if info != nil {
			bytesTotal = info.TotalLength()
			piecesTotal = info.NumPieces()
		}

		bytesCompleted := t.BytesCompleted()
		progress := 0.0
		if bytesTotal > 0 {
			progress = float64(bytesCompleted) / float64(bytesTotal) * 100
		}

		piecesCompleted := 0
		if info != nil {
			for i := 0; i < piecesTotal; i++ {
				if t.PieceState(i).Complete {
					piecesCompleted++
				}
			}
		}

		peersConnected := len(t.PeerConns())

		// Read raw cumulative bytes for reporter to compute speed externally
		tStats := t.Stats()

		stats := &TorrentStats{
			InfoHash:        t.InfoHash().HexString(),
			BytesCompleted:  bytesCompleted,
			BytesTotal:      bytesTotal,
			PeersConnected:  peersConnected,
			PeersTotal:      peersConnected,
			Progress:        progress,
			PiecesCompleted: piecesCompleted,
			PiecesTotal:     piecesTotal,
			// Speed and ETA left at 0 — reporter fills these in
			rawBytesRead:    tStats.BytesReadData.Int64(),
			rawBytesWritten: tStats.BytesWrittenData.Int64(),
		}
		stats.IsSeeding = at.IsSeeding
		stats.IsDownloading = at.IsDownloading
		stats.IsErrored = at.IsErrored
		stats.ErrorMessage = at.ErrorMessage
		stats.HasTransfer = at.TransferID != ""
		result[hash] = stats
	}

	return result
}

// ActivityInfo contains torrent activity data for reporting (no import cycles)
type ActivityInfo struct {
	InfoHash       string
	PackageName    string
	IsSeeding      bool
	IsDownloading  bool
	IsErrored      bool
	ErrorMessage   string
	HasTransfer    bool
	Progress       float64
	BytesCompleted int64
	BytesTotal     int64
	PeersConnected int
}

// GetActivityStats returns activity info for all torrents (used by WebSocket activity reporter).
// Does NOT touch speedSamples — uses raw bytes for speed-free snapshot only.
func (c *Client) GetActivityStats() []ActivityInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]ActivityInfo, 0, len(c.torrents))
	for _, at := range c.torrents {
		t := at.Torrent
		info := t.Info()

		var bytesTotal int64
		if info != nil {
			bytesTotal = info.TotalLength()
		}
		bytesCompleted := t.BytesCompleted()
		progress := 0.0
		if bytesTotal > 0 {
			progress = float64(bytesCompleted) / float64(bytesTotal) * 100
		}
		peersConnected := len(t.PeerConns())

		packageName := ""
		if info != nil {
			packageName = info.Name
		}

		result = append(result, ActivityInfo{
			InfoHash:       t.InfoHash().HexString(),
			PackageName:    packageName,
			IsSeeding:      at.IsSeeding,
			IsDownloading:  at.IsDownloading,
			IsErrored:      at.IsErrored,
			ErrorMessage:   at.ErrorMessage,
			HasTransfer:    at.TransferID != "",
			Progress:       progress,
			BytesCompleted: bytesCompleted,
			BytesTotal:     bytesTotal,
			PeersConnected: peersConnected,
		})
	}

	return result
}

// StopTorrent stops seeding or downloading a torrent
func (c *Client) StopTorrent(infoHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	at, exists := c.torrents[infoHash]
	if !exists {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	// Log WHY the torrent is being stopped (stack trace would be ideal but at least log it)
	infoName := ""
	if at.Torrent.Info() != nil {
		infoName = at.Torrent.Info().Name
	}
	log.Printf("[SEED-HEALTH] ⚠ StopTorrent CALLED for %s (%s) — IsSeeding=%v IsDownloading=%v age=%s",
		infoHash[:12], infoName, at.IsSeeding, at.IsDownloading, time.Since(at.AddedAt).Round(time.Second))

	// Drop torrent
	at.Torrent.Drop()

	// Remove from active torrents
	delete(c.torrents, infoHash)

	// Clean up speed sample
	c.speedMu.Lock()
	delete(c.speedSamples, infoHash)
	c.speedMu.Unlock()

	return nil
}

// PauseTorrent pauses downloading a torrent by cancelling all piece requests
// and setting max connections to 0. The torrent stays loaded so it can be resumed.
func (c *Client) PauseTorrent(infoHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	at, exists := c.torrents[infoHash]
	if !exists {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	t := at.Torrent
	numPieces := t.NumPieces()

	// Cancel all piece download requests
	t.CancelPieces(0, numPieces)

	// Block new peer connections
	t.SetMaxEstablishedConns(0)

	at.IsDownloading = false
	log.Printf("[torrent] PAUSED %s (%s) — cancelled %d pieces, blocked connections",
		infoHash[:12], t.Info().Name, numPieces)

	return nil
}

// ResumeTorrent resumes a paused torrent by re-requesting all pieces and allowing connections
func (c *Client) ResumeTorrent(infoHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	at, exists := c.torrents[infoHash]
	if !exists {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	t := at.Torrent

	// Restore connections (default is 50 in anacrolix/torrent)
	t.SetMaxEstablishedConns(50)

	// Re-request all pieces
	t.DownloadAll()

	at.IsDownloading = true
	log.Printf("[torrent] RESUMED %s (%s) — re-requesting all pieces, connections restored",
		infoHash[:12], t.Info().Name)

	return nil
}

// registerSeeder registers this server as a seeder in the database
func (c *Client) registerSeeder(torrentID, localPath, status string) error {
	query := `
		INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, last_announce, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (torrent_id, server_id)
		DO UPDATE SET local_path = $4, status = $5, last_announce = $6, updated_at = $8
	`

	id := uuid.New().String()
	now := time.Now()

	_, err := c.db.Exec(query, id, torrentID, c.serverID, localPath, status, now, now, now)
	return err
}

// createIngestionRecord creates an initial ingestion tracking record when a download completes.
// This record is used by the IngestionDetector to track RosettaBridge ingestion progress.
func (c *Client) createIngestionRecord(packageID, infoHash, downloadPath string) {
	if c.db == nil {
		return
	}

	_, err := c.db.Exec(`
		INSERT INTO dcp_ingestion_status (id, server_id, package_id, info_hash, download_path, status)
		VALUES (uuid_generate_v4(), $1, $2, $3, $4, 'downloaded')
		ON CONFLICT (server_id, package_id) DO NOTHING
	`, c.serverID, packageID, infoHash, downloadPath)

	if err != nil {
		log.Printf("[ingestion] Error creating ingestion record for package %s: %v", packageID, err)
	} else {
		log.Printf("[ingestion] Created ingestion tracking record for package %s (info_hash=%s)", packageID, infoHash[:12])
	}
}

// GetActiveTorrents returns a snapshot of active torrents for external use (e.g. ingestion detector)
func (c *Client) GetActiveTorrents() map[string]*ActiveTorrent {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*ActiveTorrent, len(c.torrents))
	for k, v := range c.torrents {
		result[k] = v
	}
	return result
}

// updateTransferProgress updates transfer progress in database
func (c *Client) updateTransferProgress(transferID string, stats *TorrentStats) error {
	query := `
		UPDATE transfers
		SET progress_percent = $1,
		    downloaded_bytes = $2,
		    download_speed_bps = $3,
		    upload_speed_bps = $4,
		    peers_connected = $5,
		    eta_seconds = $6,
		    updated_at = $7
		WHERE id = $8
	`

	_, err := c.db.Exec(query, stats.Progress, stats.BytesCompleted, stats.DownloadSpeed,
		stats.UploadSpeed, stats.PeersConnected, stats.ETA, time.Now(), transferID)

	return err
}

// updateTransferError marks a transfer as failed with an error message.
// Uses the error reporter callback if set (client mode → HTTP to main server),
// otherwise falls back to direct DB update (main server mode).
func (c *Client) updateTransferError(transferID, errorMessage string) {
	// Try the remote error reporter first (used on client machines)
	if c.errorReporter != nil {
		if err := c.errorReporter(transferID, "error", errorMessage); err != nil {
			log.Printf("[download] Error reporting transfer error via API for %s: %v", transferID, err)
		} else {
			log.Printf("[download] Transfer %s error reported to main server: %s", transferID, errorMessage)
			return
		}
	}

	// Fall back to direct DB update (main server or if API call failed)
	query := `
		UPDATE transfers
		SET status = 'error',
		    error_message = $1,
		    updated_at = $2
		WHERE id = $3
	`
	_, err := c.db.Exec(query, errorMessage, time.Now(), transferID)
	if err != nil {
		log.Printf("[download] Error updating transfer error for %s: %v", transferID, err)
	} else {
		log.Printf("[download] Transfer %s marked as error: %s", transferID, errorMessage)
	}
}

// updateTransferStatus updates transfer status
func (c *Client) updateTransferStatus(transferID, status string) error {
	var completedAt sql.NullTime
	var startedAt sql.NullTime

	if status == "completed" {
		completedAt.Time = time.Now()
		completedAt.Valid = true
	}

	if status == "downloading" {
		startedAt.Time = time.Now()
		startedAt.Valid = true
	}

	query := `
		UPDATE transfers
		SET status = $1,
		    completed_at = COALESCE($2, completed_at),
		    started_at = COALESCE($3, started_at),
		    updated_at = $4
		WHERE id = $5
	`

	_, err := c.db.Exec(query, status, completedAt, startedAt, time.Now(), transferID)
	return err
}

// RepairPieceCompletion fixes corrupted piece completion data at startup.
// When duplicate processes ran simultaneously, one may have written completed=false
// for pieces that are actually valid on disk. This deletes those false entries
// so the library will re-verify them on next startup.
func (c *Client) RepairPieceCompletion() {
	log.Printf("[repair] Checking for corrupted piece completion data...")

	// Delete completed=false entries for torrents where this server has the full DCP on disk.
	// The library will re-verify these pieces from disk (reads + SHA1) and write true results.
	// This is safe because:
	// 1. If the data IS on disk, re-verification will mark them true
	// 2. If the data is NOT on disk, re-verification will mark them false again
	query := `
		DELETE FROM torrent_piece_completion
		WHERE completed = false
		  AND info_hash IN (
		    SELECT dt.info_hash
		    FROM dcp_torrents dt
		    JOIN server_dcp_inventory inv ON inv.package_id = dt.package_id AND inv.server_id = $1
		  )
	`

	result, err := c.db.Exec(query, c.serverID)
	if err != nil {
		log.Printf("[repair] Error cleaning piece completion: %v", err)
		return
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("[repair] Deleted %d stale completed=false piece entries — library will re-verify from disk", affected)
	} else {
		log.Printf("[repair] No corrupted piece completion data found")
	}
}

// SeedExisting loads all existing torrents for this server from the database and starts seeding them.
// Call this at startup to resume seeding after a restart.
func (c *Client) SeedExisting() {
	query := `
		SELECT t.id, t.package_id, t.info_hash, t.torrent_file,
		       COALESCE(dp.package_name, '') AS package_name,
		       inv.local_path
		FROM dcp_torrents t
		JOIN dcp_packages dp ON dp.id = t.package_id
		JOIN server_dcp_inventory inv ON inv.package_id = t.package_id AND inv.server_id = $1
		WHERE t.torrent_file IS NOT NULL
		ORDER BY t.created_at ASC
	`

	rows, err := c.db.Query(query, c.serverID)
	if err != nil {
		log.Printf("SeedExisting: failed to query torrents: %v", err)
		return
	}
	defer rows.Close()

	var seeded, skipped, failed int
	for rows.Next() {
		var torrentID, packageID, infoHash, packageName, localPath string
		var torrentFile []byte
		if err := rows.Scan(&torrentID, &packageID, &infoHash, &torrentFile, &packageName, &localPath); err != nil {
			log.Printf("SeedExisting: scan error: %v", err)
			failed++
			continue
		}

		err := c.StartSeeding(torrentFile, localPath, packageID, torrentID)
		if err != nil {
			log.Printf("SeedExisting: failed to seed %s (%s): %v", packageName, infoHash, err)
			failed++
			continue
		}

		// Check if already counted as skip (StartSeeding returns nil for already-seeding)
		c.mu.RLock()
		at, exists := c.torrents[infoHash]
		c.mu.RUnlock()
		if exists && at != nil {
			seeded++
			log.Printf("  Seeding: %s (info_hash=%s)", packageName, infoHash)
		} else {
			skipped++
		}
	}

	log.Printf("SeedExisting complete: %d torrents seeding, %d skipped, %d failed", seeded, skipped, failed)

	// Log detailed stats after a delay so piece verification has time to run
	go func() {
		time.Sleep(30 * time.Second)
		c.LogDetailedStats()
	}()
}

// ResumeDownloads resumes all in-progress downloads for this server after restart.
// Queries the transfers table for active downloads targeting this server,
// fetches the torrent file from the database, and calls StartDownload for each.
// PostgreSQL piece completion ensures already-downloaded pieces are not re-downloaded.
func (c *Client) ResumeDownloads() {
	log.Printf("[resume-downloads] Checking for in-progress downloads to resume (serverID=%s, scanPath=%s)...", c.serverID, c.scanPath)

	query := `
		SELECT t.id, t.torrent_id, dt.torrent_file, dt.package_id,
		       COALESCE(dp.package_name, '') AS package_name,
		       t.downloaded_bytes, t.status
		FROM transfers t
		JOIN dcp_torrents dt ON dt.id = t.torrent_id
		JOIN dcp_packages dp ON dp.id = dt.package_id
		WHERE t.destination_server_id = $1
		  AND t.status IN ('downloading', 'active')
		  AND dt.torrent_file IS NOT NULL
		ORDER BY t.priority DESC, t.created_at ASC
	`

	rows, err := c.db.Query(query, c.serverID)
	if err != nil {
		log.Printf("[resume-downloads] Failed to query transfers: %v", err)
		return
	}
	defer rows.Close()

	var resumed, failed int
	for rows.Next() {
		var transferID, torrentID, packageID, packageName, status string
		var torrentFile []byte
		var downloadedBytes int64
		if err := rows.Scan(&transferID, &torrentID, &torrentFile, &packageID, &packageName, &downloadedBytes, &status); err != nil {
			log.Printf("[resume-downloads] Scan error: %v", err)
			failed++
			continue
		}

		destPath := filepath.Join(c.scanPath, packageName)

		log.Printf("[resume-downloads] Found transfer %s: package=%q status=%s downloaded=%d bytes -> %s (torrent_file=%d bytes)",
			transferID, packageName, status, downloadedBytes, destPath, len(torrentFile))

		err := c.StartDownload(torrentFile, destPath, packageID, transferID)
		if err != nil {
			log.Printf("[resume-downloads] Failed to resume %s: %v", transferID, err)
			failed++
			continue
		}
		log.Printf("[resume-downloads] Successfully resumed transfer %s (%s)", transferID, packageName)
		resumed++
	}

	log.Printf("[resume-downloads] Complete: %d resumed, %d failed", resumed, failed)
}

// StartSeederMaintenance periodically re-registers all seeding torrents with the tracker
// to prevent cleanupPeers() from removing them (10 min timeout).
// Only needed on the main server where the in-process tracker runs.
func (c *Client) StartSeederMaintenance(ctx context.Context) {
	if c.tracker == nil {
		return
	}

	ticker := time.NewTicker(4 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("SeederMaintenance: context cancelled, stopping")
			return
		case <-ticker.C:
			c.mu.RLock()
			count := 0
			skipped := 0
			for _, at := range c.torrents {
				if at.IsSeeding && at.SeederPeerID != "" {
					libSeeding := at.Torrent.Seeding()
					if !libSeeding {
						log.Printf("SeederMaintenance: ⚠ torrent %s flag=seeding but library.Seeding()=false, re-announcing anyway",
							at.InfoHash[:12])
					}
					bytesLeft := at.Torrent.Length() - at.Torrent.BytesCompleted()
					if bytesLeft < 0 {
						bytesLeft = 0
					}
					c.announceToTracker(at.Torrent.InfoHash(), at.SeederPeerID, bytesLeft)
					count++
				} else if at.IsSeeding && at.SeederPeerID == "" {
					log.Printf("SeederMaintenance: ⚠ torrent %s IsSeeding=true but SeederPeerID is empty, cannot re-announce",
						at.InfoHash[:12])
					skipped++
				}
			}
			c.mu.RUnlock()
			if count > 0 || skipped > 0 {
				log.Printf("SeederMaintenance: re-announced %d seeders to tracker (%d skipped)", count, skipped)
			}
		}
	}
}

// StartIntegrityWatcher periodically checks that torrent data files still exist on disk.
// If files have been deleted, it clears piece completion, re-adds the torrent, and resumes downloading.
// It only triggers ONCE per torrent — the integrityReset flag prevents a re-trigger loop.
func (c *Client) StartIntegrityWatcher(ctx context.Context) {
	log.Println("[integrity] Starting file integrity watcher (checks every 30s)...")
	time.Sleep(15 * time.Second)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		c.checkFileIntegrity()
		select {
		case <-ctx.Done():
			log.Println("[integrity] Watcher stopped")
			return
		case <-ticker.C:
		}
	}
}

// checkFileIntegrity verifies that torrent data files exist on disk for all active torrents.
func (c *Client) checkFileIntegrity() {
	c.mu.RLock()
	torrents := make([]*ActiveTorrent, 0, len(c.torrents))
	for _, at := range c.torrents {
		torrents = append(torrents, at)
	}
	c.mu.RUnlock()

	for _, at := range torrents {
		// Skip errored torrents and those we've already reset
		if at.IsErrored || at.integrityReset {
			continue
		}

		t := at.Torrent
		info := t.Info()
		if info == nil || at.LocalPath == "" {
			continue
		}

		// Check if the data directory exists
		if _, err := os.Stat(at.LocalPath); err != nil {
			if !os.IsNotExist(err) {
				continue
			}

			log.Printf("[integrity] DETECTED: data directory DELETED for %s (%s) at %s",
				at.InfoHash[:12], info.Name, at.LocalPath)

			// Mark so we don't re-trigger every 30s
			c.mu.Lock()
			at.integrityReset = true
			c.mu.Unlock()

			c.clearPieceCompletion(at.InfoHash)

			if at.TransferID != "" {
				// Re-create directories and re-verify
				parentDir := filepath.Dir(at.LocalPath)
				os.MkdirAll(at.LocalPath, 0755)
				for _, f := range info.Files {
					if len(f.Path) > 1 {
						subdir := filepath.Join(parentDir, filepath.Join(f.Path[:len(f.Path)-1]...))
						os.MkdirAll(subdir, 0755)
					}
				}
				c.reverifyTorrent(at)
			} else if at.IsSeeding {
				log.Printf("[integrity] %s: seeding torrent data DELETED — marking as errored", at.InfoHash[:12])
				c.mu.Lock()
				at.IsSeeding = false
				at.IsErrored = true
				at.ErrorMessage = "Data files deleted from disk"
				c.mu.Unlock()
			}
			continue
		}

		// Directory exists — for downloading torrents, spot-check files
		if at.TransferID != "" {
			missingFiles := 0
			for _, f := range info.Files {
				fp := filepath.Join(filepath.Dir(at.LocalPath), filepath.Join(f.Path...))
				if _, err := os.Stat(fp); os.IsNotExist(err) {
					missingFiles++
				}
			}
			totalFiles := len(info.Files)

			if missingFiles > 0 && missingFiles == totalFiles && t.BytesCompleted() > 0 {
				log.Printf("[integrity] DETECTED: all %d files missing for %s (%s) — data was deleted",
					totalFiles, at.InfoHash[:12], info.Name)

				c.mu.Lock()
				at.integrityReset = true
				c.mu.Unlock()

				c.clearPieceCompletion(at.InfoHash)
				c.reverifyTorrent(at)
			}
		}
	}
}

// clearPieceCompletion removes all piece completion records for a torrent.
func (c *Client) clearPieceCompletion(infoHash string) {
	if c.db == nil {
		return
	}
	result, err := c.db.Exec("DELETE FROM torrent_piece_completion WHERE info_hash = $1", infoHash)
	if err != nil {
		log.Printf("[integrity] Error clearing piece completion for %s: %v", infoHash[:12], err)
		return
	}
	affected, _ := result.RowsAffected()
	log.Printf("[integrity] Cleared %d piece completion records for %s", affected, infoHash[:12])
}

// reverifyTorrent drops and re-adds a torrent to force the library to re-verify all pieces.
// Uses the stored AnnounceURL (not the metainfo's, which may be empty after re-add).
func (c *Client) reverifyTorrent(at *ActiveTorrent) {
	infoHash := at.InfoHash
	t := at.Torrent

	// Get metainfo BEFORE dropping
	mi := t.Metainfo()

	// Use the stored announce URL (the metainfo may lose it after drop+re-add)
	announceURL := at.AnnounceURL
	if announceURL == "" {
		announceURL = mi.Announce
	}
	if announceURL == "" {
		log.Printf("[integrity] WARNING: no announce URL for %s — torrent will not find peers", infoHash[:12])
	}

	// Drop the existing torrent
	t.Drop()

	// Re-add with fresh storage
	parentDir := filepath.Dir(at.LocalPath)
	completion := NewPostgresPieceCompletion(c.db, t.InfoHash())
	torrentStorage := storage.NewFileWithCompletion(parentDir, completion)

	newT, _, err := c.client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoHash:  t.InfoHash(),
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{announceURL}},
		Storage:   torrentStorage,
	})
	if err != nil {
		log.Printf("[integrity] Error re-adding torrent %s: %v", infoHash[:12], err)
		return
	}

	<-newT.GotInfo()

	// Update the ActiveTorrent reference
	c.mu.Lock()
	at.Torrent = newT
	c.mu.Unlock()

	// Set up write error handler again
	transferID := at.TransferID
	newT.SetOnWriteChunkError(func(writeErr error) {
		errMsg := writeErr.Error()
		count := atomic.AddInt32(&at.writeErrCount, 1)
		if count <= 3 || count%50 == 0 {
			log.Printf("[download] WRITE CHUNK ERROR #%d for %s: %v", count, infoHash[:12], writeErr)
		}

		if strings.Contains(errMsg, "no such file or directory") {
			parts := strings.SplitN(errMsg, ": ", 2)
			if len(parts) >= 1 {
				filePath := strings.TrimPrefix(parts[0], "open ")
				dir := filepath.Dir(filePath)
				if mkdirErr := os.MkdirAll(dir, 0755); mkdirErr == nil {
					newT.AllowDataDownload()
					atomic.StoreInt32(&at.writeErrCount, 0)
					return
				}
			}
		}

		isFatal := strings.Contains(errMsg, "read-only file system") ||
			strings.Contains(errMsg, "permission denied") ||
			strings.Contains(errMsg, "no space left on device")

		if isFatal || count >= 50 {
			c.mu.Lock()
			if !at.IsErrored {
				at.IsErrored = true
				at.IsDownloading = false
				if strings.Contains(errMsg, "read-only file system") {
					at.ErrorMessage = "Disk is read-only — cannot write downloaded data"
				} else if strings.Contains(errMsg, "permission denied") {
					at.ErrorMessage = "Permission denied — cannot write to download directory"
				} else if strings.Contains(errMsg, "no space left on device") {
					at.ErrorMessage = "Disk full — no space left on device"
				} else {
					at.ErrorMessage = fmt.Sprintf("Disk write error: %s", errMsg)
				}
				c.updateTransferError(transferID, at.ErrorMessage)
			}
			c.mu.Unlock()
			return
		}

		time.Sleep(2 * time.Second)
		newT.AllowDataDownload()
	})

	if at.TransferID != "" {
		newT.DownloadAll()
		log.Printf("[integrity] %s: re-verification started, downloading all pieces (announce=%s)",
			infoHash[:12], announceURL)
	}

	log.Printf("[integrity] %s: torrent re-added and re-verifying (%d bytes completed)",
		infoHash[:12], newT.BytesCompleted())
}

// StartSeedHealthMonitor runs a periodic health check on all seeding torrents.
// It logs comprehensive status for every torrent and detects when a torrent
// has silently stopped seeding at the library level.
func (c *Client) StartSeedHealthMonitor(ctx context.Context) {
	// First report after 60 seconds, then every 2 minutes
	time.Sleep(60 * time.Second)

	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		c.runSeedHealthCheck()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runSeedHealthCheck performs a single health check iteration on all torrents.
func (c *Client) runSeedHealthCheck() {
	c.mu.RLock()
	torrents := make([]*ActiveTorrent, 0, len(c.torrents))
	for _, at := range c.torrents {
		torrents = append(torrents, at)
	}
	c.mu.RUnlock()

	if len(torrents) == 0 {
		log.Printf("[SEED-HEALTH] No active torrents loaded")
		return
	}

	log.Printf("[SEED-HEALTH] ╔══════════════════════════════════════════════════════════════════╗")
	log.Printf("[SEED-HEALTH] ║  TORRENT HEALTH REPORT — %d torrents loaded at %s  ║",
		len(torrents), time.Now().Format("15:04:05"))
	log.Printf("[SEED-HEALTH] ╚══════════════════════════════════════════════════════════════════╝")

	problemCount := 0
	for _, at := range torrents {
		t := at.Torrent
		info := t.Info()

		var bytesTotal, bytesCompleted int64
		var numPieces int
		var infoName string
		if info != nil {
			bytesTotal = info.TotalLength()
			infoName = info.Name
			numPieces = info.NumPieces()
		}
		bytesCompleted = t.BytesCompleted()

		progress := 0.0
		if bytesTotal > 0 {
			progress = float64(bytesCompleted) / float64(bytesTotal) * 100
		}

		// Get library-level seeding state (this is the ground truth)
		libSeeding := t.Seeding()
		tStats := t.Stats()
		peersConnected := len(t.PeerConns())
		knownSwarm := len(t.KnownSwarm())

		// Determine status string
		status := "OK"
		problems := []string{}

		// Check: our flag says seeding but library says no
		if at.IsSeeding && !libSeeding {
			problems = append(problems, "FLAG_MISMATCH: IsSeeding=true but library.Seeding()=false")
		}

		// Check: 100% complete but library not seeding
		if progress >= 100 && !libSeeding {
			problems = append(problems, "COMPLETE_BUT_NOT_SEEDING: 100% data but library reports not seeding")
		}

		// Check: torrent has no info
		if info == nil {
			problems = append(problems, "NO_INFO: torrent info is nil (metadata not loaded)")
		}

		// Check: half-open peers piling up (connection issues)
		if tStats.HalfOpenPeers > 10 {
			problems = append(problems, fmt.Sprintf("HIGH_HALF_OPEN: %d half-open connections", tStats.HalfOpenPeers))
		}

		if len(problems) > 0 {
			status = "PROBLEM"
			problemCount++
		}

		// Determine the DCP name to show
		dcpName := infoName
		if dcpName == "" {
			dcpName = at.PackageID
		}

		// Calculate human-readable size
		sizeStr := formatBytes(bytesTotal)
		completedStr := formatBytes(bytesCompleted)

		log.Printf("[SEED-HEALTH]   [%s] %s", status, dcpName)
		log.Printf("[SEED-HEALTH]     hash=%s progress=%.1f%% (%s / %s) pieces=%d",
			at.InfoHash[:12], progress, completedStr, sizeStr, numPieces)
		log.Printf("[SEED-HEALTH]     flags: IsSeeding=%v IsDownloading=%v | lib: Seeding()=%v",
			at.IsSeeding, at.IsDownloading, libSeeding)
		log.Printf("[SEED-HEALTH]     peers: connected=%d knownSwarm=%d halfOpen=%d pending=%d connectedSeeders=%d",
			peersConnected, knownSwarm, tStats.HalfOpenPeers, tStats.PendingPeers, tStats.ConnectedSeeders)
		log.Printf("[SEED-HEALTH]     io: chunksRead=%d chunksWritten=%d bytesReadData=%d bytesWrittenData=%d",
			tStats.ChunksRead.Int64(), tStats.ChunksWritten.Int64(),
			tStats.BytesReadData.Int64(), tStats.BytesWrittenData.Int64())
		log.Printf("[SEED-HEALTH]     age: added %s ago", time.Since(at.AddedAt).Round(time.Second))

		for _, prob := range problems {
			log.Printf("[SEED-HEALTH]     ⚠ %s", prob)
		}

		// Auto-recovery: if our flag says seeding but library disagrees and we're 100%, re-add
		if at.IsSeeding && !libSeeding && progress >= 100 {
			log.Printf("[SEED-HEALTH]     → ATTEMPTING RECOVERY: re-enabling data download for %s", at.InfoHash[:12])
			t.AllowDataDownload()
		}
	}

	if problemCount > 0 {
		log.Printf("[SEED-HEALTH] ⚠ %d torrent(s) have problems — see details above", problemCount)
	} else {
		log.Printf("[SEED-HEALTH] All %d torrent(s) healthy", len(torrents))
	}
}

// formatBytes returns a human-readable byte count string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}


// StartStatsReporter starts a background goroutine that reports stats to main server
func (c *Client) StartStatsReporter(ctx context.Context, reportFunc func(map[string]*TorrentStats) error) {
	ticker := time.NewTicker(60 * time.Second) // Report every 60 seconds for seeding
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := c.GetAllStats()
			if len(stats) > 0 {
				if err := reportFunc(stats); err != nil {
					fmt.Printf("Error reporting stats: %v\n", err)
				}
			}
		}
	}
}

// LogDetailedStats logs detailed information about all loaded torrents
func (c *Client) LogDetailedStats() {
	c.mu.RLock()
	defer c.mu.RUnlock()

	log.Printf("=== Torrent Client Detailed Stats (%d torrents loaded) ===", len(c.torrents))
	for hash, at := range c.torrents {
		t := at.Torrent
		info := t.Info()
		var bytesTotal, bytesCompleted int64
		var numPieces int
		var infoName string
		if info != nil {
			bytesTotal = info.TotalLength()
			infoName = info.Name
			numPieces = info.NumPieces()
		}
		bytesCompleted = t.BytesCompleted()
		peersConnected := len(t.PeerConns())

		progress := 0.0
		if bytesTotal > 0 {
			progress = float64(bytesCompleted) / float64(bytesTotal) * 100
		}

		seeding := at.IsSeeding
		downloading := at.IsDownloading
		libSeeding := t.Seeding()
		log.Printf("  [%s] %s: completed=%d/%d (%.1f%%) pieces=%d peers=%d seeding=%v downloading=%v lib.Seeding=%v",
			hash[:12], infoName, bytesCompleted, bytesTotal, progress, numPieces, peersConnected, seeding, downloading, libSeeding)

		// Warn if mismatch
		if seeding && !libSeeding {
			log.Printf("  [%s] ⚠ WARNING: flag says seeding but anacrolix library says NOT seeding!", hash[:12])
		}
	}
	log.Printf("=== End Torrent Stats ===")
}
