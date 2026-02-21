package relay

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// RelayDialer implements the anacrolix/torrent Dialer interface.
// It is added to the torrent client via AddDialer() and tried in parallel
// with the default TCP dialer when connecting to peers.
//
// The relay dialer adds a 1-second delay before attempting, giving the direct
// TCP dialer time to succeed first. If direct TCP works (<1s), the relay
// attempt is cancelled. If direct TCP is blocked by NAT, the relay dialer
// succeeds after ~2 seconds (1s delay + 1s relay setup).
type RelayDialer struct {
	relayAddr string // Relay server address (e.g., "main.server.com:10866")
	delay     time.Duration

	// Our own addresses — never relay-dial ourselves (would spam relay server)
	ownAddrs sync.Map // key: addr string (ip:port), value: struct{}

	// Cache of peers known to be directly reachable (skip relay for these)
	directPeers sync.Map // key: addr string, value: time.Time (when marked direct)
	// Cache of peers known to be behind NAT (skip initial delay for these)
	natPeers sync.Map // key: addr string, value: time.Time (when last attempted)
	// Cache of peers that failed relay recently — backoff to prevent spam
	// value: time.Time of last failure
	recentFails sync.Map // key: addr string, value: time.Time

	// Stats
	relayAttempts  int64
	relaySuccesses int64
	relaySkips     int64
}

// NewRelayDialer creates a new relay dialer.
func NewRelayDialer(relayAddr string) *RelayDialer {
	return &RelayDialer{
		relayAddr: relayAddr,
		delay:     1 * time.Second,
	}
}

// AddOwnAddr registers one of our own listening addresses.
// The relay dialer will never attempt to relay-connect to these addresses,
// preventing the client from spamming the relay server trying to reach itself.
func (d *RelayDialer) AddOwnAddr(addr string) {
	d.ownAddrs.Store(addr, struct{}{})
}

// Dial connects to a peer, potentially through the relay server.
// This implements the anacrolix/torrent Dialer interface.
//
// The anacrolix library calls all registered dialers in parallel for each peer.
// The first successful connection wins and the others are cancelled via context.
func (d *RelayDialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	// Never relay-dial our own addresses — this prevents the torrent client from
	// spamming the relay server when the anacrolix library tries to connect to
	// peers that are actually our own listening address (which it includes in the
	// peer list received from the tracker).
	if _, isSelf := d.ownAddrs.Load(addr); isSelf {
		return nil, errors.New("skip relay: own address")
	}

	// Skip relay for peers known to be directly reachable
	if when, ok := d.directPeers.Load(addr); ok {
		// Expire direct cache entries after 10 minutes
		if t, ok := when.(time.Time); ok && time.Since(t) < 10*time.Minute {
			atomic.AddInt64(&d.relaySkips, 1)
			return nil, errors.New("peer is directly reachable, skip relay")
		}
		// Entry expired, remove it
		d.directPeers.Delete(addr)
	}

	// Backoff for peers that have failed relay recently.
	// The relay server caches failures for 60s — if it tells us a peer is not
	// registered, we respect that and wait 90s before trying again.
	// This prevents the 10–20 requests/second spam seen when a peer is unregistered
	// but the torrent library keeps requesting connections to it (e.g. when seeding
	// many torrents and the peer appears in every tracker response).
	const failBackoff = 90 * time.Second
	if failTime, ok := d.recentFails.Load(addr); ok {
		if t, ok := failTime.(time.Time); ok && time.Since(t) < failBackoff {
			atomic.AddInt64(&d.relaySkips, 1)
			return nil, fmt.Errorf("skip relay: peer %s failed recently (%.0fs ago)", addr, time.Since(t).Seconds())
		}
		d.recentFails.Delete(addr)
	}

	// Determine if this is a known NAT-blocked peer
	delay := d.delay // Default: 1 second (gives direct TCP a chance)
	if _, ok := d.natPeers.Load(addr); ok {
		delay = 0 // Skip delay for peers already known to be behind NAT
	}

	// Wait before attempting relay — gives direct TCP dialer time to succeed.
	// If direct works (typically <1s), context is cancelled and we return early.
	// For peers already known to be behind NAT, skip the delay entirely.
	if delay > 0 {
		select {
		case <-time.After(delay):
			// Delay elapsed, direct dialer likely failed — try relay
		case <-ctx.Done():
			// Direct dialer succeeded, or overall dial was cancelled
			return nil, ctx.Err()
		}
	} else {
		// For known NAT peers, check context before proceeding
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	atomic.AddInt64(&d.relayAttempts, 1)
	RelayLog("[relay-dialer] Attempting relay connection to %s via %s", addr, d.relayAddr)

	// Connect to relay server
	rawConn, err := net.DialTimeout("tcp", d.relayAddr, ConnectTimeout)
	if err != nil {
		RelayLog("[relay-dialer] Failed to connect to relay server %s: %v", d.relayAddr, err)
		return nil, fmt.Errorf("relay server unreachable: %w", err)
	}

	// Set TCP optimizations on the relay connection for better throughput
	optimizeTCPConn(rawConn)

	// Check context again (direct may have succeeded while we were connecting)
	select {
	case <-ctx.Done():
		rawConn.Close()
		return nil, ctx.Err()
	default:
	}

	// Send RELAY-CONNECT request
	if err := SendMessage(rawConn, fmt.Sprintf("%s %s", CmdConnect, addr)); err != nil {
		rawConn.Close()
		RelayLog("[relay-dialer] Failed to send connect request for %s: %v", addr, err)
		return nil, fmt.Errorf("relay connect failed: %w", err)
	}

	// Read response (OK <session_id> or ERROR <reason>)
	//
	// CRITICAL: We use a bufio.Reader to read the OK response line. The reader
	// may buffer additional bytes beyond the newline (BitTorrent handshake data
	// sent by the remote peer through the relay). We MUST wrap the connection
	// in a bufferedConn so those bytes are not lost. Without this, the anacrolix
	// library receives a corrupted BitTorrent handshake and drops the connection
	// after ~10 seconds.
	rawConn.SetReadDeadline(time.Now().Add(DataConnTimeout))
	reader := bufio.NewReaderSize(rawConn, 4096)
	line, err := reader.ReadString('\n')
	if err != nil {
		rawConn.Close()
		RelayLog("[relay-dialer] Failed to read relay response for %s: %v", addr, err)
		return nil, fmt.Errorf("relay response failed: %w", err)
	}

	// Clear read deadline for normal BitTorrent protocol usage
	rawConn.SetReadDeadline(time.Time{})

	resp := trimSpace(line)
	cmd, arg := ParseCommand(resp)

	if cmd != CmdOK {
		rawConn.Close()
		RelayLog("[relay-dialer] Relay rejected connection to %s: %s %s", addr, cmd, arg)
		// Cache this failure so we don't hammer the relay server with the same peer.
		// The 90s backoff aligns with the relay server's 60s negative cache TTL plus margin.
		d.recentFails.Store(addr, time.Now())
		return nil, fmt.Errorf("relay rejected: %s %s", cmd, arg)
	}

	atomic.AddInt64(&d.relaySuccesses, 1)
	RelayLog("[relay-dialer] Relay connection ESTABLISHED to peer %s (session=%s)", addr, arg)

	// Mark this peer as known to be behind NAT, so future attempts skip the delay
	d.natPeers.Store(addr, time.Now())

	// Wrap the connection with the buffered reader so any bytes already buffered
	// by the bufio.Reader are properly read by the anacrolix torrent library.
	// This is CRITICAL: without this wrapper, buffered handshake bytes are lost
	// and the BitTorrent connection fails within seconds.
	wrappedConn := &bufferedConn{
		Conn:   rawConn,
		reader: reader,
	}

	return wrappedConn, nil
}

// LocalAddr returns the network address of the dialer.
// Required by the anacrolix/torrent Dialer interface.
func (d *RelayDialer) LocalAddr() net.Addr {
	return &relayDialerAddr{d.relayAddr}
}

// MarkDirectlyReachable marks a peer address as directly reachable,
// so the relay dialer will skip it in future attempts.
// Called by the download monitor when it sees successful peer connections.
func (d *RelayDialer) MarkDirectlyReachable(addr string) {
	d.directPeers.Store(addr, time.Now())
}

// GetStats returns relay dialer statistics.
func (d *RelayDialer) GetStats() (attempts, successes, skips int64) {
	return atomic.LoadInt64(&d.relayAttempts),
		atomic.LoadInt64(&d.relaySuccesses),
		atomic.LoadInt64(&d.relaySkips)
}

// relayDialerAddr implements net.Addr for the relay dialer.
type relayDialerAddr struct {
	addr string
}

func (a *relayDialerAddr) Network() string { return "tcp" }
func (a *relayDialerAddr) String() string  { return "relay-dialer:" + a.addr }

// bufferedConn wraps a net.Conn with a bufio.Reader to ensure that any bytes
// already buffered during the relay handshake are properly read by the caller.
//
// When we read the "OK <session_id>\n" response from the relay server, the
// bufio.Reader may have also read additional bytes (the start of the BitTorrent
// handshake from the remote peer). If we return the raw net.Conn, those bytes
// are lost in the bufio.Reader's internal buffer. This wrapper ensures Read()
// drains the bufio buffer first before reading from the underlying connection.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

// Read reads from the buffered reader first (draining any buffered handshake
// bytes), then from the underlying connection.
func (bc *bufferedConn) Read(b []byte) (int, error) {
	return bc.reader.Read(b)
}

// WriteTo implements io.WriterTo for efficient copying (used by io.Copy).
// This ensures that when the relay bridge or torrent client copies data,
// it drains the bufio buffer first.
func (bc *bufferedConn) WriteTo(w io.Writer) (int64, error) {
	return bc.reader.WriteTo(w)
}

// optimizeTCPConn sets TCP socket options for better relay performance.
func optimizeTCPConn(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		// Disable Nagle's algorithm — BitTorrent sends many small protocol
		// messages (handshakes, have, bitfield, request, piece) that must
		// not be delayed by Nagle buffering. Without this, small messages
		// are held for up to 200ms waiting for more data, causing latency
		// spikes and protocol timeouts.
		tc.SetNoDelay(true)

		// Enable TCP keepalive to detect dead connections faster.
		// Without this, a dead relay connection can hang for minutes
		// before the OS detects the failure.
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)

		// Increase socket buffer sizes for better throughput on bulk transfers.
		// Default is typically 87KB which is too small for high-bandwidth relay.
		tc.SetReadBuffer(256 * 1024)  // 256KB read buffer
		tc.SetWriteBuffer(256 * 1024) // 256KB write buffer
	}
}
