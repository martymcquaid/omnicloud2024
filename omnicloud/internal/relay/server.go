package relay

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// bridgeBufferSize is the buffer size used for io.CopyBuffer in relay bridges.
// Default io.Copy uses 32KB which is too small for bulk BitTorrent data transfer.
// 256KB provides much better throughput for large DCP files.
const bridgeBufferSize = 256 * 1024

// Server is a TCP relay server that bridges connections between NATted peers.
// It runs on the main server (which has a public IP) and forwards BitTorrent
// protocol traffic between seeders and downloaders that can't reach each other directly.
type Server struct {
	port        int
	maxSessions int
	listener    net.Listener

	// Registered seeders: key is advertised addr (ip:port from tracker)
	mu    sync.RWMutex
	peers map[string]*RegisteredPeer

	// Active sessions waiting for seeder data connection: key is session ID
	sessionMu sync.Mutex
	sessions  map[string]*Session

	// Negative cache for direct dial failures: key is target addr, value is time of failure.
	// Prevents flooding the network with TCP connect attempts to unreachable peers.
	directDialFailMu sync.RWMutex
	directDialFails  map[string]time.Time

	// Stats
	activeSessions  int64
	totalSessions   int64
	totalBytesIn    int64
	totalBytesOut   int64
}

// NewServer creates a new relay server.
func NewServer(port, maxSessions int) *Server {
	if port <= 0 {
		port = DefaultRelayPort
	}
	if maxSessions <= 0 {
		maxSessions = DefaultMaxSessions
	}
	return &Server{
		port:            port,
		maxSessions:     maxSessions,
		peers:           make(map[string]*RegisteredPeer),
		sessions:        make(map[string]*Session),
		directDialFails: make(map[string]time.Time),
	}
}

// Start begins listening for relay connections. Blocks until context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.port)
	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("[relay-server] failed to listen on %s: %w", addr, err)
	}
	RelayLog("[relay-server] Listening on port %d (max sessions: %d)", s.port, s.maxSessions)

	// Start cleanup goroutine for stale peers and sessions
	go s.cleanupLoop(ctx)

	// Start stats logging goroutine
	go s.statsLoop(ctx)

	// Accept connections
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				RelayLog("[relay-server] Shutting down")
				return nil
			default:
				RelayLog("[relay-server] Accept error: %v", err)
				continue
			}
		}
		go s.handleConnection(conn)
	}
}

// handleConnection reads the first message to determine connection type.
func (s *Server) handleConnection(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()

	// Set TCP optimizations on all incoming connections
	optimizeTCPConn(conn)

	// Read the initial command with a timeout
	msg, err := ReadMessage(conn, 10*time.Second)
	if err != nil {
		RelayLog("[relay-server] Failed to read initial message from %s: %v", remoteAddr, err)
		conn.Close()
		return
	}

	cmd, arg := ParseCommand(msg)

	switch cmd {
	case CmdRegister:
		s.handleRegister(conn, arg, remoteAddr)
	case CmdConnect:
		s.handleConnect(conn, arg, remoteAddr)
	case CmdSession:
		s.handleSession(conn, arg, remoteAddr)
	default:
		RelayLog("[relay-server] Unknown command from %s: %q", remoteAddr, msg)
		SendMessage(conn, fmt.Sprintf("%s unknown command", CmdError))
		conn.Close()
	}
}

// handleRegister processes a seeder registration (persistent control connection).
func (s *Server) handleRegister(conn net.Conn, advertisedAddr string, remoteAddr string) {
	if advertisedAddr == "" {
		RelayLog("[relay-server] Register from %s: missing advertised address", remoteAddr)
		SendMessage(conn, fmt.Sprintf("%s missing address", CmdError))
		conn.Close()
		return
	}

	RelayLog("[relay-server] Seeder registering: advertised=%s remote=%s", advertisedAddr, remoteAddr)

	// Close any existing registration for this address
	s.mu.Lock()
	if existing, ok := s.peers[advertisedAddr]; ok {
		RelayLog("[relay-server] Replacing existing registration for %s", advertisedAddr)
		existing.ControlConn.Close()
	}
	peer := &RegisteredPeer{
		AdvertisedAddr: advertisedAddr,
		ControlConn:    conn,
		RegisteredAt:   time.Now(),
		LastPing:       time.Now(),
	}
	s.peers[advertisedAddr] = peer
	s.mu.Unlock()

	if err := SendMessage(conn, CmdOK); err != nil {
		RelayLog("[relay-server] Failed to send OK to seeder %s: %v", advertisedAddr, err)
		s.removePeer(advertisedAddr)
		conn.Close()
		return
	}

	RelayLog("[relay-server] Seeder registered: peer=%s control_conn=active", advertisedAddr)

	// Handle control connection (keepalive loop)
	s.controlLoop(peer)
}

// controlLoop manages the persistent control connection to a seeder.
func (s *Server) controlLoop(peer *RegisteredPeer) {
	defer func() {
		s.removePeer(peer.AdvertisedAddr)
		peer.ControlConn.Close()
		RelayLog("[relay-server] Seeder disconnected: peer=%s", peer.AdvertisedAddr)
	}()

	reader := bufio.NewReader(peer.ControlConn)

	// Start ping ticker
	pingTicker := time.NewTicker(PingInterval)
	defer pingTicker.Stop()

	// Use a channel for reads so we can also handle pings
	msgCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			msg, err := ReadMessageFromReader(reader, peer.ControlConn, ControlReadTimeout)
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- msg
		}
	}()

	for {
		select {
		case msg := <-msgCh:
			cmd, _ := ParseCommand(msg)
			switch cmd {
			case CmdPong:
				peer.LastPing = time.Now()
			case CmdPing:
				// Client sent ping, respond with pong
				SendMessage(peer.ControlConn, CmdPong)
				peer.LastPing = time.Now()
			default:
				RelayLog("[relay-server] Unexpected message on control conn for %s: %q", peer.AdvertisedAddr, msg)
			}

		case err := <-errCh:
			RelayLog("[relay-server] Control connection error for %s: %v", peer.AdvertisedAddr, err)
			return

		case <-pingTicker.C:
			if err := SendMessage(peer.ControlConn, CmdPing); err != nil {
				RelayLog("[relay-server] Ping failed for %s: %v", peer.AdvertisedAddr, err)
				return
			}
		}
	}
}

// handleConnect processes a downloader's request to connect to a seeder.
func (s *Server) handleConnect(conn net.Conn, targetAddr string, remoteAddr string) {
	if targetAddr == "" {
		RelayLog("[relay-server] Connect from %s: missing target address", remoteAddr)
		SendMessage(conn, fmt.Sprintf("%s missing target address", CmdError))
		conn.Close()
		return
	}

	RelayLog("[relay-server] Connect request: downloader=%s wants peer=%s", remoteAddr, targetAddr)

	// Check session limit
	active := atomic.LoadInt64(&s.activeSessions)
	if int(active) >= s.maxSessions {
		RelayLog("[relay-server] Session limit reached (%d/%d), rejecting connect from %s",
			active, s.maxSessions, remoteAddr)
		SendMessage(conn, fmt.Sprintf("%s relay at capacity", CmdError))
		conn.Close()
		return
	}

	// Look up the seeder
	s.mu.RLock()
	peer, exists := s.peers[targetAddr]
	s.mu.RUnlock()

	if !exists {
		// Peer not registered via control connection.
		// Check negative cache before attempting direct dial — prevents flooding
		// the network with TCP connections that will all timeout.
		s.directDialFailMu.RLock()
		failTime, failed := s.directDialFails[targetAddr]
		s.directDialFailMu.RUnlock()

		if failed && time.Since(failTime) < 60*time.Second {
			// Recently failed — don't retry, just reject immediately
			RelayLog("[relay-server] Peer %s not registered, direct dial cached as failed (%.0fs ago) — rejecting",
				targetAddr, time.Since(failTime).Seconds())
			SendMessage(conn, fmt.Sprintf("%s peer not registered (cached failure)", CmdError))
			conn.Close()
			return
		}

		// Try direct TCP connection as fallback
		RelayLog("[relay-server] Peer %s not registered — attempting direct dial fallback (requested by %s)", targetAddr, remoteAddr)
		s.handleDirectDial(conn, targetAddr, remoteAddr)
		return
	}

	// Create session
	sessionID := NewSessionID()
	session := &Session{
		ID:             sessionID,
		TargetAddr:     targetAddr,
		CreatedAt:      time.Now(),
		DownloaderConn: conn,
	}

	s.sessionMu.Lock()
	s.sessions[sessionID] = session
	s.sessionMu.Unlock()

	// Ask seeder to open a data connection
	RelayLog("[relay-server] Requesting session %s from seeder %s for downloader %s",
		sessionID, targetAddr, remoteAddr)

	err := SendMessage(peer.ControlConn, fmt.Sprintf("%s %s", CmdSessionRequest, sessionID))
	if err != nil {
		RelayLog("[relay-server] Failed to send session request to seeder %s: %v", targetAddr, err)
		s.removeSession(sessionID)
		SendMessage(conn, fmt.Sprintf("%s seeder unreachable", CmdError))
		conn.Close()
		return
	}

	// Wait for seeder to open data connection (handled in handleSession)
	// Set a timeout - if seeder doesn't connect in time, fail
	go func() {
		time.Sleep(DataConnTimeout)
		s.sessionMu.Lock()
		sess, exists := s.sessions[sessionID]
		if exists && sess.SeederConn == nil {
			RelayLog("[relay-server] Session %s timed out waiting for seeder data connection", sessionID)
			delete(s.sessions, sessionID)
			s.sessionMu.Unlock()
			SendMessage(conn, fmt.Sprintf("%s session timeout", CmdError))
			conn.Close()
		} else {
			s.sessionMu.Unlock()
		}
	}()
}

// handleDirectDial connects directly to a peer and bridges the connection.
// Used when the peer is not registered via control connection but may be
// reachable from the relay server (e.g., the main server's own torrent port,
// or any peer on the same network as the relay server).
func (s *Server) handleDirectDial(downloaderConn net.Conn, targetAddr string, remoteAddr string) {
	seederConn, err := net.DialTimeout("tcp", targetAddr, ConnectTimeout)
	if err != nil {
		RelayLog("[relay-server] Direct dial fallback FAILED for %s: %v (peer unreachable from relay too)", targetAddr, err)

		// Cache the failure to prevent repeated attempts (60-second TTL)
		s.directDialFailMu.Lock()
		s.directDialFails[targetAddr] = time.Now()
		s.directDialFailMu.Unlock()

		SendMessage(downloaderConn, fmt.Sprintf("%s peer not registered and not directly reachable", CmdError))
		downloaderConn.Close()
		return
	}

	// Set TCP optimizations on the direct-dialed connection
	optimizeTCPConn(seederConn)

	sessionID := NewSessionID()
	RelayLog("[relay-server] Direct dial fallback SUCCESS for %s — session %s bridging %s <-> %s",
		targetAddr, sessionID, targetAddr, remoteAddr)

	// Send OK to downloader so the relay dialer knows the connection is established
	if err := SendMessage(downloaderConn, fmt.Sprintf("%s %s", CmdOK, sessionID)); err != nil {
		RelayLog("[relay-server] Failed to send OK to downloader for direct-dial session %s: %v", sessionID, err)
		seederConn.Close()
		downloaderConn.Close()
		return
	}

	// Bridge the two connections
	session := &Session{
		ID:             sessionID,
		TargetAddr:     targetAddr,
		CreatedAt:      time.Now(),
		DownloaderConn: downloaderConn,
		SeederConn:     seederConn,
	}

	atomic.AddInt64(&s.activeSessions, 1)
	atomic.AddInt64(&s.totalSessions, 1)
	go s.bridge(session)
}

// handleSession processes a seeder's data connection for an established session.
func (s *Server) handleSession(conn net.Conn, sessionID string, remoteAddr string) {
	if sessionID == "" {
		RelayLog("[relay-server] Session from %s: missing session ID", remoteAddr)
		SendMessage(conn, fmt.Sprintf("%s missing session ID", CmdError))
		conn.Close()
		return
	}

	s.sessionMu.Lock()
	session, exists := s.sessions[sessionID]
	if !exists {
		s.sessionMu.Unlock()
		RelayLog("[relay-server] Session %s not found (may have timed out)", sessionID)
		SendMessage(conn, fmt.Sprintf("%s session not found", CmdError))
		conn.Close()
		return
	}

	if session.SeederConn != nil {
		s.sessionMu.Unlock()
		RelayLog("[relay-server] Session %s already has seeder connection", sessionID)
		SendMessage(conn, fmt.Sprintf("%s session already connected", CmdError))
		conn.Close()
		return
	}

	session.SeederConn = conn
	delete(s.sessions, sessionID) // Remove from pending sessions
	s.sessionMu.Unlock()

	// Send OK to seeder
	if err := SendMessage(conn, CmdOK); err != nil {
		RelayLog("[relay-server] Failed to send OK to seeder for session %s: %v", sessionID, err)
		conn.Close()
		session.DownloaderConn.Close()
		return
	}

	// Send OK to downloader
	if err := SendMessage(session.DownloaderConn, fmt.Sprintf("%s %s", CmdOK, sessionID)); err != nil {
		RelayLog("[relay-server] Failed to send OK to downloader for session %s: %v", sessionID, err)
		conn.Close()
		session.DownloaderConn.Close()
		return
	}

	RelayLog("[relay-server] Session %s established: bridging %s <-> %s",
		sessionID, session.TargetAddr, session.DownloaderConn.RemoteAddr())

	// Bridge the two connections
	atomic.AddInt64(&s.activeSessions, 1)
	atomic.AddInt64(&s.totalSessions, 1)
	s.bridge(session)
}

// bridge forwards data bidirectionally between two connections.
// Uses 256KB buffers for high-throughput bulk data transfer.
func (s *Server) bridge(session *Session) {
	defer func() {
		session.DownloaderConn.Close()
		session.SeederConn.Close()
		atomic.AddInt64(&s.activeSessions, -1)
	}()

	startTime := time.Now()
	var bytesIn, bytesOut int64

	// Copy in both directions concurrently
	done := make(chan struct{}, 2)

	// Seeder → Downloader (this is the bulk data direction for downloads)
	go func() {
		buf := make([]byte, bridgeBufferSize)
		n, _ := io.CopyBuffer(session.DownloaderConn, session.SeederConn, buf)
		atomic.AddInt64(&bytesOut, n)
		atomic.AddInt64(&s.totalBytesOut, n)
		// Signal EOF to the other direction
		if tc, ok := session.DownloaderConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()

	// Downloader → Seeder (BitTorrent request messages, relatively small)
	go func() {
		buf := make([]byte, bridgeBufferSize)
		n, _ := io.CopyBuffer(session.SeederConn, session.DownloaderConn, buf)
		atomic.AddInt64(&bytesIn, n)
		atomic.AddInt64(&s.totalBytesIn, n)
		if tc, ok := session.SeederConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()

	// Wait for both directions to complete
	<-done
	<-done

	elapsed := time.Since(startTime)
	totalBytes := bytesIn + bytesOut
	RelayLog("[relay-server] Session %s closed: transferred %s in %s (seeder→dl: %s, dl→seeder: %s)",
		session.ID,
		formatBytes(totalBytes),
		elapsed.Truncate(time.Second),
		formatBytes(bytesOut),
		formatBytes(bytesIn))
}

// removePeer removes a seeder registration.
func (s *Server) removePeer(addr string) {
	s.mu.Lock()
	delete(s.peers, addr)
	s.mu.Unlock()
}

// removeSession removes a pending session.
func (s *Server) removeSession(sessionID string) {
	s.sessionMu.Lock()
	delete(s.sessions, sessionID)
	s.sessionMu.Unlock()
}

// cleanupLoop periodically removes stale peers, sessions, and expired negative cache entries.
func (s *Server) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			for addr, peer := range s.peers {
				if time.Since(peer.LastPing) > ControlReadTimeout {
					RelayLog("[relay-server] Removing stale peer %s (last ping: %s ago)",
						addr, time.Since(peer.LastPing).Truncate(time.Second))
					peer.ControlConn.Close()
					delete(s.peers, addr)
				}
			}
			s.mu.Unlock()

			s.sessionMu.Lock()
			for id, sess := range s.sessions {
				if time.Since(sess.CreatedAt) > SessionSetupTimeout {
					RelayLog("[relay-server] Removing stale session %s (created: %s ago)",
						id, time.Since(sess.CreatedAt).Truncate(time.Second))
					if sess.DownloaderConn != nil {
						sess.DownloaderConn.Close()
					}
					if sess.SeederConn != nil {
						sess.SeederConn.Close()
					}
					delete(s.sessions, id)
				}
			}
			s.sessionMu.Unlock()

			// Clean up expired negative cache entries (older than 60 seconds)
			s.directDialFailMu.Lock()
			for addr, failTime := range s.directDialFails {
				if time.Since(failTime) > 60*time.Second {
					delete(s.directDialFails, addr)
				}
			}
			s.directDialFailMu.Unlock()
		}
	}
}

// statsLoop periodically logs relay server statistics.
func (s *Server) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			numPeers := len(s.peers)
			peerList := make([]string, 0, numPeers)
			for addr := range s.peers {
				peerList = append(peerList, addr)
			}
			s.mu.RUnlock()

			active := atomic.LoadInt64(&s.activeSessions)
			total := atomic.LoadInt64(&s.totalSessions)
			bytesIn := atomic.LoadInt64(&s.totalBytesIn)
			bytesOut := atomic.LoadInt64(&s.totalBytesOut)

			RelayLog("[relay-server] Stats: registered_peers=%d active_sessions=%d total_sessions=%d bytes_relayed=%s peers=%v",
				numPeers, active, total, formatBytes(bytesIn+bytesOut), peerList)
		}
	}
}

// GetRegisteredPeerCount returns the number of registered peers.
func (s *Server) GetRegisteredPeerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.peers)
}

// GetActiveSessionCount returns the number of active relay sessions.
func (s *Server) GetActiveSessionCount() int64 {
	return atomic.LoadInt64(&s.activeSessions)
}

// IsPeerRegistered checks if a peer with the given address is registered.
func (s *Server) IsPeerRegistered(addr string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.peers[addr]
	return exists
}

// formatBytes converts bytes to a human-readable string.
func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1fMB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1fKB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%dB", b)
	}
}
