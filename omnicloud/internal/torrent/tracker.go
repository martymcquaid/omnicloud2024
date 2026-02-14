package torrent

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
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

// AnnounceResponse is the tracker response to an announce request
type AnnounceResponse struct {
	Interval    int    `bencode:"interval"`
	Complete    int    `bencode:"complete"`    // Seeders
	Incomplete  int    `bencode:"incomplete"`  // Leechers
	Peers       []Peer `bencode:"peers"`       // For bencode dict model
	FailureReason string `bencode:"failure reason,omitempty"`
}

// PeerCompact represents a peer in compact format
type PeerCompact struct {
	IP   [4]byte
	Port uint16
}

// NewTracker creates a new BitTorrent tracker
func NewTracker(db *sql.DB, interval int) *Tracker {
	if interval <= 0 {
		interval = 60 // Default 60 seconds
	}

	return &Tracker{
		db:       db,
		swarms:   make(map[string]*Swarm),
		interval: interval,
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
		t.sendError(w, "Invalid info_hash")
		return
	}
	infoHash := hex.EncodeToString(infoHashBytes)

	peerID := query.Get("peer_id")
	if peerID == "" {
		t.sendError(w, "Missing peer_id")
		return
	}

	port, err := strconv.Atoi(query.Get("port"))
	if err != nil || port <= 0 || port > 65535 {
		t.sendError(w, "Invalid port")
		return
	}

	uploaded, _ := strconv.ParseInt(query.Get("uploaded"), 10, 64)
	downloaded, _ := strconv.ParseInt(query.Get("downloaded"), 10, 64)
	left, _ := strconv.ParseInt(query.Get("left"), 10, 64)

	event := query.Get("event") // started, completed, stopped

	// Get peer IP
	ip := t.getPeerIP(r)

	// Handle the announce
	response := t.handleAnnounce(infoHash, peerID, ip, port, uploaded, downloaded, left, event)

	// Send response
	w.Header().Set("Content-Type", "text/plain")
	bencode.NewEncoder(w).Encode(response)
}

// handleAnnounce processes an announce request
func (t *Tracker) handleAnnounce(infoHash, peerID, ip string, port int, uploaded, downloaded, left int64, event string) *AnnounceResponse {
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
	var peerList []Peer

	for _, p := range swarm.Peers {
		if p.PeerID == peerID {
			continue // Don't send peer to itself
		}

		if p.Left == 0 {
			complete++
		} else {
			incomplete++
		}

		peerList = append(peerList, *p)
	}

	return &AnnounceResponse{
		Interval:   t.interval,
		Complete:   complete,
		Incomplete: incomplete,
		Peers:      peerList,
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
