package torrent

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

// Tracker implements a simple private HTTP BitTorrent tracker
type Tracker struct {
	db       *sql.DB
	mu       sync.RWMutex
	swarms   map[string]*Swarm // key: info_hash (hex)
	interval int               // Announce interval in seconds
	publicIP string            // Public IP to substitute for loopback addresses
}

// Swarm represents peers for a single torrent
type Swarm struct {
	InfoHash string
	Peers    map[string]*Peer // key: peer_id
	mu       sync.RWMutex
}

// Peer represents a peer in a swarm
type Peer struct {
	PeerID     string
	IP         string
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64
	LastSeen   time.Time
}

// PeerDict represents a peer in dictionary format for bencode (used by some clients)
type PeerDict struct {
	PeerID string `bencode:"peer id"`
	IP     string `bencode:"ip"`
	Port   int    `bencode:"port"`
}

// AnnounceResponse is the tracker response to an announce request
type AnnounceResponse struct {
	Interval      int         `bencode:"interval"`
	Complete      int         `bencode:"complete"`   // Seeders
	Incomplete    int         `bencode:"incomplete"` // Leechers
	Peers         interface{} `bencode:"peers"`      // Can be compact string or list of dicts
	FailureReason string      `bencode:"failure reason,omitempty"`
}

// PeerCompact represents a peer in compact format
type PeerCompact struct {
	IP   [4]byte
	Port uint16
}

// PeerSnapshot is a read-only view of a tracked peer for API consumers.
type PeerSnapshot struct {
	PeerID     string    `json:"peer_id"`
	IP         string    `json:"ip"`
	Port       int       `json:"port"`
	Uploaded   int64     `json:"uploaded"`
	Downloaded int64     `json:"downloaded"`
	Left       int64     `json:"left"`
	LastSeen   time.Time `json:"last_seen"`
	IsSeeder   bool      `json:"is_seeder"`
}

// SwarmSnapshot is a read-only view of a tracker swarm.
type SwarmSnapshot struct {
	InfoHash     string         `json:"info_hash"`
	Seeders      int            `json:"seeders"`
	Leechers     int            `json:"leechers"`
	PeersCount   int            `json:"peers_count"`
	LastAnnounce *time.Time     `json:"last_announce,omitempty"`
	Peers        []PeerSnapshot `json:"peers"`
}

// TrackerSnapshot is a read-only view of current tracker state.
type TrackerSnapshot struct {
	IntervalSec  int             `json:"interval_sec"`
	ActiveSwarms int             `json:"active_swarms"`
	TotalPeers   int             `json:"total_peers"`
	GeneratedAt  time.Time       `json:"generated_at"`
	Swarms       []SwarmSnapshot `json:"swarms"`
}

// NewTracker creates a new BitTorrent tracker
func NewTracker(db *sql.DB, interval int, publicIP string) *Tracker {
	if interval <= 0 {
		interval = 60 // Default 60 seconds
	}

	return &Tracker{
		db:       db,
		swarms:   make(map[string]*Swarm),
		interval: interval,
		publicIP: publicIP,
	}
}

// ServeHTTP handles HTTP tracker requests
func (t *Tracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only handle /announce endpoint
	if r.URL.Path != "/announce" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Extract required parameters
	infoHashBytes, err := parseInfoHash(query.Get("info_hash"))
	if err != nil {
		t.logAnnounceAttempt("", query.Get("peer_id"), t.getPeerIP(r), 0, query.Get("event"), "error", "Invalid info_hash")
		t.sendError(w, "Invalid info_hash")
		return
	}
	infoHash := hex.EncodeToString(infoHashBytes)

	peerID := query.Get("peer_id")
	if peerID == "" {
		t.logAnnounceAttempt(infoHash, "", t.getPeerIP(r), 0, query.Get("event"), "error", "Missing peer_id")
		t.sendError(w, "Missing peer_id")
		return
	}

	port, err := strconv.Atoi(query.Get("port"))
	if err != nil || port <= 0 || port > 65535 {
		t.logAnnounceAttempt(infoHash, peerID, t.getPeerIP(r), 0, query.Get("event"), "error", "Invalid port")
		t.sendError(w, "Invalid port")
		return
	}

	uploaded, _ := strconv.ParseInt(query.Get("uploaded"), 10, 64)
	downloaded, _ := strconv.ParseInt(query.Get("downloaded"), 10, 64)
	left, _ := strconv.ParseInt(query.Get("left"), 10, 64)

	event := query.Get("event") // started, completed, stopped
	
	// Check if client wants compact peer format (most modern clients do)
	compact := query.Get("compact") != "0" // Default to compact=1

	// Get peer IP
	ip := t.getPeerIP(r)

	// Handle the announce
	response := t.handleAnnounce(infoHash, peerID, ip, port, uploaded, downloaded, left, event, compact)
	t.logAnnounceAttempt(infoHash, peerID, ip, port, event, "ok", "")

	// Send response
	w.Header().Set("Content-Type", "text/plain")
	bencode.NewEncoder(w).Encode(response)
}

func (t *Tracker) logAnnounceAttempt(infoHash, peerID, ip string, port int, event, status, failureReason string) {
	if t.db == nil {
		return
	}
	_, _ = t.db.Exec(`
		INSERT INTO torrent_announce_attempts (info_hash, peer_id, ip, port, event, status, failure_reason, created_at)
		VALUES (NULLIF($1, ''), NULLIF($2, ''), NULLIF($3, ''), $4, NULLIF($5, ''), $6, NULLIF($7, ''), NOW())
	`, infoHash, peerID, ip, port, event, status, failureReason)
}

// handleAnnounce processes an announce request
func (t *Tracker) handleAnnounce(infoHash, peerID, ip string, port int, uploaded, downloaded, left int64, event string, compact bool) *AnnounceResponse {
	// Get or create swarm
	t.mu.Lock()
	swarm, exists := t.swarms[infoHash]
	if !exists {
		swarm = &Swarm{
			InfoHash: infoHash,
			Peers:    make(map[string]*Peer),
		}
		t.swarms[infoHash] = swarm
	}
	t.mu.Unlock()

	// Handle peer
	swarm.mu.Lock()
	defer swarm.mu.Unlock()

	if event == "stopped" {
		// Remove peer
		delete(swarm.Peers, peerID)
	} else {
		// Add or update peer
		peer, exists := swarm.Peers[peerID]
		if !exists {
			peer = &Peer{
				PeerID: peerID,
				IP:     ip,
				Port:   port,
			}
			swarm.Peers[peerID] = peer
		}

		peer.Uploaded = uploaded
		peer.Downloaded = downloaded
		peer.Left = left
		peer.LastSeen = time.Now()
	}

	// Count seeders and leechers
	complete := 0
	incomplete := 0

	// Collect peers (excluding the requesting peer)
	var peersToSend []*Peer
	for _, p := range swarm.Peers {
		if p.PeerID == peerID {
			continue // Don't send peer to itself
		}

		if p.Left == 0 {
			complete++
		} else {
			incomplete++
		}

		peersToSend = append(peersToSend, p)
	}

	// Build peer response in appropriate format
	var peers interface{}
	if compact {
		// Compact format: 6 bytes per peer (4 bytes IPv4 + 2 bytes port)
		// Note: IPv6 requires compact=0 or peers6 field
		compactPeers := make([]byte, 0, len(peersToSend)*6)
		for _, p := range peersToSend {
			peerIP := p.IP
			// Convert IPv6 loopback to IPv4 loopback for local testing
			if peerIP == "::1" || peerIP == "::ffff:127.0.0.1" {
				peerIP = "127.0.0.1"
			}
			ipAddr := net.ParseIP(peerIP)
			if ipAddr == nil {
				continue
			}
			ipv4 := ipAddr.To4()
			if ipv4 == nil {
				continue // Skip IPv6 addresses in compact mode
			}
			compactPeers = append(compactPeers, ipv4...)
			compactPeers = append(compactPeers, byte(p.Port>>8), byte(p.Port&0xff))
		}
		peers = string(compactPeers)
	} else {
		// Dictionary format for clients that don't support compact
		peerDicts := make([]PeerDict, 0, len(peersToSend))
		for _, p := range peersToSend {
			peerDicts = append(peerDicts, PeerDict{
				PeerID: p.PeerID,
				IP:     p.IP,
				Port:   p.Port,
			})
		}
		peers = peerDicts
	}

	return &AnnounceResponse{
		Interval:   t.interval,
		Complete:   complete,
		Incomplete: incomplete,
		Peers:      peers,
	}
}

// getPeerIP extracts the peer's IP address from the request
func (t *Tracker) getPeerIP(r *http.Request) string {
	// Check for X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// Check for X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Use remote address
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)

	// Translate loopback addresses to public IP so external peers can connect
	if t.publicIP != "" && (ip == "127.0.0.1" || ip == "::1" || ip == "::ffff:127.0.0.1") {
		return t.publicIP
	}

	return ip
}

// parseInfoHash parses the URL-encoded info_hash parameter
func parseInfoHash(encoded string) ([]byte, error) {
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return nil, err
	}

	if len(decoded) != 20 {
		return nil, fmt.Errorf("info_hash must be 20 bytes, got %d", len(decoded))
	}

	return []byte(decoded), nil
}

// sendError sends an error response
func (t *Tracker) sendError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/plain")
	response := &AnnounceResponse{
		FailureReason: message,
	}
	bencode.NewEncoder(w).Encode(response)
}

// Start starts the tracker HTTP server
func (t *Tracker) Start(addr string) error {
	// Clean up old peers periodically
	go t.cleanupPeers()

	fmt.Printf("Starting BitTorrent tracker on %s\n", addr)
	return http.ListenAndServe(addr, t)
}

// cleanupPeers removes peers that haven't announced in a while
func (t *Tracker) cleanupPeers() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		timeout := time.Now().Add(-10 * time.Minute)

		t.mu.RLock()
		for _, swarm := range t.swarms {
			swarm.mu.Lock()
			for peerID, peer := range swarm.Peers {
				if peer.LastSeen.Before(timeout) {
					delete(swarm.Peers, peerID)
				}
			}
			swarm.mu.Unlock()
		}
		t.mu.RUnlock()
	}
}

// GetSwarmStats returns statistics for a swarm
func (t *Tracker) GetSwarmStats(infoHash string) (seeders, leechers int) {
	t.mu.RLock()
	swarm, exists := t.swarms[infoHash]
	t.mu.RUnlock()

	if !exists {
		return 0, 0
	}

	swarm.mu.RLock()
	defer swarm.mu.RUnlock()

	for _, peer := range swarm.Peers {
		if peer.Left == 0 {
			seeders++
		} else {
			leechers++
		}
	}

	return seeders, leechers
}

// GetAllSwarms returns info about all active swarms
func (t *Tracker) GetAllSwarms() map[string]map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]map[string]interface{})

	for hash, swarm := range t.swarms {
		swarm.mu.RLock()
		seeders := 0
		leechers := 0
		for _, peer := range swarm.Peers {
			if peer.Left == 0 {
				seeders++
			} else {
				leechers++
			}
		}
		swarm.mu.RUnlock()

		result[hash] = map[string]interface{}{
			"seeders":  seeders,
			"leechers": leechers,
			"peers":    len(swarm.Peers),
		}
	}

	return result
}

// GetSnapshot returns a consistent in-memory tracker snapshot for UI/API consumption.
func (t *Tracker) GetSnapshot() TrackerSnapshot {
	snapshot := TrackerSnapshot{
		IntervalSec: t.interval,
		GeneratedAt: time.Now(),
		Swarms:      make([]SwarmSnapshot, 0),
	}

	t.mu.RLock()
	for hash, swarm := range t.swarms {
		swarm.mu.RLock()
		ss := SwarmSnapshot{
			InfoHash:   hash,
			Peers:      make([]PeerSnapshot, 0, len(swarm.Peers)),
			PeersCount: len(swarm.Peers),
		}
		var latest time.Time
		for _, peer := range swarm.Peers {
			isSeeder := peer.Left == 0
			if isSeeder {
				ss.Seeders++
			} else {
				ss.Leechers++
			}
			if peer.LastSeen.After(latest) {
				latest = peer.LastSeen
			}
			ss.Peers = append(ss.Peers, PeerSnapshot{
				PeerID:     peer.PeerID,
				IP:         peer.IP,
				Port:       peer.Port,
				Uploaded:   peer.Uploaded,
				Downloaded: peer.Downloaded,
				Left:       peer.Left,
				LastSeen:   peer.LastSeen,
				IsSeeder:   isSeeder,
			})
		}
		swarm.mu.RUnlock()

		if !latest.IsZero() {
			ts := latest
			ss.LastAnnounce = &ts
		}
		sort.Slice(ss.Peers, func(i, j int) bool {
			return ss.Peers[i].LastSeen.After(ss.Peers[j].LastSeen)
		})
		snapshot.TotalPeers += ss.PeersCount
		snapshot.Swarms = append(snapshot.Swarms, ss)
	}
	t.mu.RUnlock()

	sort.Slice(snapshot.Swarms, func(i, j int) bool {
		left := snapshot.Swarms[i]
		right := snapshot.Swarms[j]
		if left.LastAnnounce == nil && right.LastAnnounce == nil {
			return left.InfoHash < right.InfoHash
		}
		if left.LastAnnounce == nil {
			return false
		}
		if right.LastAnnounce == nil {
			return true
		}
		return left.LastAnnounce.After(*right.LastAnnounce)
	})
	snapshot.ActiveSwarms = len(snapshot.Swarms)

	return snapshot
}
