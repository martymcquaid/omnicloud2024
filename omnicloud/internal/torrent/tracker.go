package torrent

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
)

const trackerDebugLogPath = "/home/appbox/DCPCLOUDAPP/.cursor/debug.log"

func writeDebugLog(hypothesisId, location, message string, data map[string]interface{}) {
	payload := map[string]interface{}{
		"hypothesisId": hypothesisId,
		"location":     location,
		"message":      message,
		"data":         data,
		"timestamp":    time.Now().Unix() * 1000,
	}
	line, _ := json.Marshal(payload)
	f, err := os.OpenFile(trackerDebugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(line)
	f.WriteString("\n")
}

// Tracker implements a simple private HTTP BitTorrent tracker
type Tracker struct {
	db           *sql.DB
	mu           sync.RWMutex
	swarms       map[string]*Swarm // key: info_hash (hex)
	interval     int               // Announce interval in seconds
	announceHost string            // When announcer is loopback, advertise this IP/host so peers can connect (e.g. auto-detected public IP)

	// Relay server info — included in announce responses so clients know where the relay is
	relayHost string
	relayPort int
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
	Interval      int    `bencode:"interval"`
	Complete      int    `bencode:"complete"`   // Seeders
	Incomplete    int    `bencode:"incomplete"` // Leechers
	Peers         []byte `bencode:"peers"`      // Compact format: 6 bytes per peer (4-byte IP + 2-byte port)
	FailureReason string `bencode:"failure reason,omitempty"`
	// Relay server info for NAT traversal — clients use this to connect to NATted peers
	RelayHost string `bencode:"relay-host,omitempty"`
	RelayPort int    `bencode:"relay-port,omitempty"`
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

// NewTracker creates a new BitTorrent tracker. announceHost is used when the
// announcer is loopback (e.g. main server seeding on same host): the tracker
// advertises this IP/host to peers so they can connect. If empty, the tracker
// falls back to OMNICLOUD_TRACKER_ANNOUNCE_HOST env var. Typically pass an
// auto-detected public IP from the main server at startup.
func NewTracker(db *sql.DB, interval int, announceHost string) *Tracker {
	if interval <= 0 {
		interval = 60 // Default 60 seconds
	}

	return &Tracker{
		db:           db,
		swarms:       make(map[string]*Swarm),
		interval:     interval,
		announceHost: strings.TrimSpace(announceHost),
	}
}

// SetRelayInfo configures relay server info that will be included in announce responses.
// Clients use this to discover the relay server for NAT traversal.
func (t *Tracker) SetRelayInfo(host string, port int) {
	t.relayHost = host
	t.relayPort = port
	log.Printf("[tracker] Relay info set: host=%s port=%d (will be included in announce responses)", host, port)
}

// ServeHTTP handles HTTP tracker requests
func (t *Tracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Debug endpoint: dump all swarms as JSON
	if r.URL.Path == "/debug/swarms" {
		t.handleDebugSwarms(w, r)
		return
	}

	// Only handle /announce endpoint
	if r.URL.Path != "/announce" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	rawInfoHash := query.Get("info_hash")
	peerID := query.Get("peer_id")
	event := query.Get("event") // started, completed, stopped

	// Extract required parameters
	infoHashBytes, err := parseInfoHash(rawInfoHash)
	if err != nil {
		t.logAnnounceAttempt("", peerID, t.getPeerIP(r), 0, event, "error", "Invalid info_hash")
		t.sendError(w, "Invalid info_hash")
		return
	}
	infoHash := hex.EncodeToString(infoHashBytes)

	if peerID == "" {
		t.logAnnounceAttempt(infoHash, "", t.getPeerIP(r), 0, event, "error", "Missing peer_id")
		t.sendError(w, "Missing peer_id")
		return
	}

	port, err := strconv.Atoi(query.Get("port"))
	if err != nil || port <= 0 || port > 65535 {
		t.logAnnounceAttempt(infoHash, peerID, t.getPeerIP(r), 0, event, "error", "Invalid port")
		t.sendError(w, "Invalid port")
		return
	}

	uploaded, _ := strconv.ParseInt(query.Get("uploaded"), 10, 64)
	downloaded, _ := strconv.ParseInt(query.Get("downloaded"), 10, 64)
	left, _ := strconv.ParseInt(query.Get("left"), 10, 64)

	// Get peer IP
	ip := t.getPeerIP(r)

	// #region agent log
	writeDebugLog("H_announce_received", "tracker.go:ServeHTTP", "tracker received announce", map[string]interface{}{
		"info_hash": infoHash, "peer_id": peerID, "ip": ip, "port": port, "event": event,
	})
	if ip == "127.0.0.1" || ip == "::1" {
		writeDebugLog("H_loopback_peer", "tracker.go:ServeHTTP", "announcer is loopback; downloaders get this IP unless OMNICLOUD_TRACKER_ANNOUNCE_HOST is set", map[string]interface{}{
			"info_hash": infoHash, "ip": ip, "announce_host_set": os.Getenv("OMNICLOUD_TRACKER_ANNOUNCE_HOST") != "",
		})
	}
	// #endregion

	// Handle the announce
	response := t.handleAnnounce(infoHash, peerID, ip, port, uploaded, downloaded, left, event)
	if response != nil && response.FailureReason != "" {
		t.logAnnounceAttempt(infoHash, peerID, ip, port, event, "error", response.FailureReason)
		log.Printf("[TRACKER] Announce FAIL: hash=%s...%s peer=%s ip=%s:%d event=%s err=%s",
			infoHash[:8], infoHash[len(infoHash)-4:], peerID[:12], ip, port, event, response.FailureReason)
	} else {
		t.logAnnounceAttempt(infoHash, peerID, ip, port, event, "ok", "")
		peersReturned := 0
		if response != nil {
			peersReturned = len(response.Peers) / 6
		}
		role := "leecher"
		if left == 0 {
			role = "seeder"
		}
		log.Printf("[TRACKER] Announce OK: hash=%s...%s ip=%s:%d role=%s event=%s → returning %d peers (complete=%d incomplete=%d)",
			infoHash[:8], infoHash[len(infoHash)-4:], ip, port, role, event, peersReturned,
			response.Complete, response.Incomplete)
	}

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

	// Count seeders and leechers and build peer list in compact format
	complete := 0
	incomplete := 0
	var peerList []byte // Compact peer format: 6 bytes per peer (4-byte IP + 2-byte port)
	var peerIPsIncluded []string

	for _, p := range swarm.Peers {
		if p.Left == 0 {
			complete++
		} else {
			incomplete++
		}

		if p.PeerID == peerID {
			continue // Don't send peer to itself
		}

		// Parse IP address (compact format requires IPv4)
		ipParts := net.ParseIP(p.IP)
		if ipParts == nil {
			// #region agent log
			writeDebugLog("H_skip_peer", "tracker.go:handleAnnounce", "skip peer parse failed", map[string]interface{}{"info_hash": infoHash, "peer_id": p.PeerID, "peer_ip": p.IP})
			// #endregion
			continue
		}
		ipv4 := ipParts.To4()
		if ipv4 == nil {
			// #region agent log
			writeDebugLog("H_skip_ipv6", "tracker.go:handleAnnounce", "skip peer IPv6 (compact format is IPv4 only)", map[string]interface{}{"info_hash": infoHash, "peer_id": p.PeerID, "peer_ip": p.IP})
			// #endregion
			continue
		}

		peerIPsIncluded = append(peerIPsIncluded, p.IP)
		// Add to compact peer list: 4 bytes IP + 2 bytes port (big endian)
		peerList = append(peerList, ipv4[0], ipv4[1], ipv4[2], ipv4[3])
		peerList = append(peerList, byte(p.Port>>8), byte(p.Port&0xFF))
	}

	// #region agent log
	writeDebugLog("H_peer_response", "tracker.go:handleAnnounce", "response built", map[string]interface{}{
		"info_hash": infoHash, "complete": complete, "incomplete": incomplete,
		"peers_returned": len(peerList) / 6, "peer_ips": peerIPsIncluded,
	})
	// #endregion

	resp := &AnnounceResponse{
		Interval:   t.interval,
		Complete:   complete,
		Incomplete: incomplete,
		Peers:      peerList, // Send as compact binary string
	}

	// Include relay server info if configured (for NAT traversal)
	if t.relayHost != "" && t.relayPort > 0 {
		resp.RelayHost = t.relayHost
		resp.RelayPort = t.relayPort
	}

	return resp
}

func (t *Tracker) logAnnounceAttempt(infoHash, peerID, ip string, port int, event, status, failureReason string) {
	if t.db == nil {
		return
	}
	query := `
		INSERT INTO torrent_announce_attempts
		    (info_hash, peer_id, ip, port, event, status, failure_reason, created_at)
		VALUES
		    (NULLIF($1, ''), NULLIF($2, ''), NULLIF($3, ''), $4, NULLIF($5, ''), $6, NULLIF($7, ''), NOW())
	`
	if _, err := t.db.Exec(query, infoHash, peerID, ip, port, event, status, failureReason); err != nil {
		// Tracker behavior should not fail because telemetry write failed.
	}
}

// getPeerIP extracts the peer's IP address from the request.
// When the request is from loopback (127.0.0.1 or ::1), e.g. the main server seeding on the same
// host as the tracker, we advertise a reachable IP: t.announceHost (auto-detected at startup),
// or OMNICLOUD_TRACKER_ANNOUNCE_HOST env var, so remote peers can connect.
func (t *Tracker) getPeerIP(r *http.Request) string {
	var ip string
	// X-Forwarded-For can be "client, proxy1, proxy2" — use first (original client)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip = strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if ip == "" {
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			ip = strings.TrimSpace(xri)
		}
	}
	if ip == "" {
		ip, _, _ = net.SplitHostPort(r.RemoteAddr)
	}
	// When the announcer is localhost (main server seeding on same host), advertise a reachable IP
	if ip != "" && (ip == "127.0.0.1" || ip == "::1") {
		host := t.announceHost
		if host == "" {
			host = os.Getenv("OMNICLOUD_TRACKER_ANNOUNCE_HOST")
		}
		if host != "" {
			host = strings.TrimSpace(host)
			if parsed := net.ParseIP(host); parsed != nil {
				return parsed.String()
			}
			// Try to resolve hostname to IPv4 so compact peer list works
			if addrs, err := net.LookupIP(host); err == nil {
				for _, a := range addrs {
					if a4 := a.To4(); a4 != nil {
						return a4.String()
					}
				}
			}
			return host
		}
	}
	return ip
}

// RegisterSeeder registers a peer directly (no HTTP round-trip).
// Use this when the seeder is in the same process as the tracker.
// The seeder is registered with both the public IP (for external clients) and
// 127.0.0.1 (for clients on the same host that can't reach the public IP via NAT).
// bytesLeft should be 0 for a complete seeder, or the actual bytes remaining for a partial seeder.
func (t *Tracker) RegisterSeeder(infoHashHex, peerID, ip string, port int, bytesLeft int64) {
	// Register with the public IP for external clients
	t.handleAnnounce(infoHashHex, peerID, ip, port, 0, 0, bytesLeft, "started")

	// Also register with 127.0.0.1 for local clients on the same host
	if ip != "127.0.0.1" {
		localPeerID := peerID + "-local"
		if len(localPeerID) > 20 {
			localPeerID = localPeerID[:20]
		}
		t.handleAnnounce(infoHashHex, localPeerID, "127.0.0.1", port, 0, 0, bytesLeft, "started")
	}

	// Log swarm state after registration
	t.mu.RLock()
	swarm, exists := t.swarms[infoHashHex]
	t.mu.RUnlock()
	if exists {
		swarm.mu.RLock()
		seeders := 0
		for _, p := range swarm.Peers {
			if p.Left == 0 {
				seeders++
			}
		}
		log.Printf("[TRACKER] RegisterSeeder: hash=%s...%s peerID=%s ip=%s port=%d left=%d → swarm has %d total peers (%d seeders)",
			infoHashHex[:8], infoHashHex[len(infoHashHex)-4:], peerID, ip, port, bytesLeft, len(swarm.Peers), seeders)
		swarm.mu.RUnlock()
	}
}

// DumpSwarms logs the current state of all swarms for debugging
func (t *Tracker) DumpSwarms() {
	t.mu.RLock()
	defer t.mu.RUnlock()

	log.Printf("[TRACKER] === SWARM DUMP: %d swarms ===", len(t.swarms))
	for hash, swarm := range t.swarms {
		swarm.mu.RLock()
		seeders := 0
		leechers := 0
		for _, p := range swarm.Peers {
			if p.Left == 0 {
				seeders++
			} else {
				leechers++
			}
		}
		log.Printf("[TRACKER]   Swarm %s...%s: %d peers (%d seeders, %d leechers)",
			hash[:8], hash[len(hash)-4:], len(swarm.Peers), seeders, leechers)
		for pid, p := range swarm.Peers {
			age := time.Since(p.LastSeen).Round(time.Second)
			role := "leecher"
			if p.Left == 0 {
				role = "SEEDER"
			}
			log.Printf("[TRACKER]     peer=%s ip=%s:%d %s left=%d lastSeen=%s ago",
				pid, p.IP, p.Port, role, p.Left, age)
		}
		swarm.mu.RUnlock()
	}
	log.Printf("[TRACKER] === END SWARM DUMP ===")
}

// handleDebugSwarms returns a JSON dump of all tracker swarms for debugging
func (t *Tracker) handleDebugSwarms(w http.ResponseWriter, r *http.Request) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	type debugPeer struct {
		PeerID   string `json:"peer_id"`
		IP       string `json:"ip"`
		Port     int    `json:"port"`
		Left     int64  `json:"left"`
		Role     string `json:"role"`
		LastSeen string `json:"last_seen"`
		Age      string `json:"age"`
	}

	type debugSwarm struct {
		InfoHash string      `json:"info_hash"`
		Seeders  int         `json:"seeders"`
		Leechers int         `json:"leechers"`
		Peers    []debugPeer `json:"peers"`
	}

	var swarms []debugSwarm
	for hash, swarm := range t.swarms {
		swarm.mu.RLock()
		ds := debugSwarm{InfoHash: hash}
		for pid, p := range swarm.Peers {
			role := "leecher"
			if p.Left == 0 {
				role = "seeder"
				ds.Seeders++
			} else {
				ds.Leechers++
			}
			ds.Peers = append(ds.Peers, debugPeer{
				PeerID:   pid,
				IP:       p.IP,
				Port:     p.Port,
				Left:     p.Left,
				Role:     role,
				LastSeen: p.LastSeen.Format(time.RFC3339),
				Age:      time.Since(p.LastSeen).Round(time.Second).String(),
			})
		}
		swarm.mu.RUnlock()
		swarms = append(swarms, ds)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(swarms)
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
		removed := 0
		totalPeers := 0
		totalSwarms := 0

		t.mu.RLock()
		totalSwarms = len(t.swarms)
		for hash, swarm := range t.swarms {
			swarm.mu.Lock()
			totalPeers += len(swarm.Peers)
			for peerID, peer := range swarm.Peers {
				if peer.LastSeen.Before(timeout) {
					role := "leecher"
					if peer.Left == 0 {
						role = "SEEDER"
					}
					log.Printf("[TRACKER] cleanupPeers: removing stale %s %s from swarm %s...%s (ip=%s:%d, lastSeen=%s ago)",
						role, peerID, hash[:8], hash[len(hash)-4:], peer.IP, peer.Port, time.Since(peer.LastSeen).Round(time.Second))
					delete(swarm.Peers, peerID)
					removed++
				}
			}
			swarm.mu.Unlock()
		}
		t.mu.RUnlock()

		log.Printf("[TRACKER] cleanupPeers: checked %d swarms, %d total peers, removed %d stale", totalSwarms, totalPeers, removed)
		if removed > 0 {
			// Dump swarms after cleanup so we can see the resulting state
			t.DumpSwarms()
		}
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
