package relay

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client maintains a persistent control connection to the relay server
// and opens data connections on demand when the relay requests sessions.
// It runs on any server that detects it is behind NAT.
type Client struct {
	relayAddr      string // Relay server address (e.g., "main.server.com:10866")
	advertisedAddr string // Our advertised ip:port from the tracker

	mu          sync.RWMutex
	controlConn net.Conn
	connected   bool

	// Channel for handing relay session connections to the torrent client listener
	sessionConns chan net.Conn

	// Stats
	sessionsHandled int64
	reconnects      int64
}

// NewClient creates a new relay client.
func NewClient(relayAddr, advertisedAddr string) *Client {
	return &Client{
		relayAddr:      relayAddr,
		advertisedAddr: advertisedAddr,
		sessionConns:   make(chan net.Conn, 50), // buffer relay sessions
	}
}

// Start connects to the relay server and maintains the control connection.
// Blocks until context is cancelled. Reconnects automatically on disconnection.
func (c *Client) Start(ctx context.Context) {
	RelayLog("[relay-client] Starting relay client: relay=%s advertised=%s", c.relayAddr, c.advertisedAddr)

	backoff := 5 * time.Second
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			c.disconnect()
			RelayLog("[relay-client] Shutting down")
			return
		default:
		}

		connStart := time.Now()
		err := c.connectAndRun(ctx)
		if err != nil {
			RelayLog("[relay-client] Connection error: %v", err)
		}

		c.disconnect()
		atomic.AddInt64(&c.reconnects, 1)

		// Reset backoff if we were connected for at least 60 seconds
		// (indicates the connection was working, not an immediate failure)
		if time.Since(connStart) > 60*time.Second {
			backoff = 5 * time.Second
		}

		// Backoff before reconnecting
		RelayLog("[relay-client] Control connection lost, reconnecting in %s...", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Increase backoff up to max
		backoff = backoff * 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// connectAndRun establishes a control connection and runs the message loop.
func (c *Client) connectAndRun(ctx context.Context) error {
	RelayLog("[relay-client] Connecting to relay server at %s...", c.relayAddr)
	conn, err := net.DialTimeout("tcp", c.relayAddr, ConnectTimeout)
	if err != nil {
		return fmt.Errorf("failed to connect to relay server %s: %w", c.relayAddr, err)
	}

	// Set TCP optimizations on control connection
	optimizeTCPConn(conn)

	c.mu.Lock()
	c.controlConn = conn
	c.connected = true
	c.mu.Unlock()

	// Send registration
	RelayLog("[relay-client] Registering as %s", c.advertisedAddr)
	if err := SendMessage(conn, fmt.Sprintf("%s %s", CmdRegister, c.advertisedAddr)); err != nil {
		return fmt.Errorf("failed to send register: %w", err)
	}

	// Read OK response
	resp, err := ReadMessage(conn, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to read register response: %w", err)
	}
	cmd, arg := ParseCommand(resp)
	if cmd != CmdOK {
		return fmt.Errorf("registration rejected: %s %s", cmd, arg)
	}

	RelayLog("[relay-client] Control connection established, registered as %s", c.advertisedAddr)

	// Reset backoff on successful connection
	return c.controlLoop(ctx, conn)
}

// controlLoop handles messages on the control connection.
func (c *Client) controlLoop(ctx context.Context, conn net.Conn) error {
	reader := bufio.NewReader(conn)

	msgCh := make(chan string, 1)
	errCh := make(chan error, 1)

	go func() {
		for {
			msg, err := ReadMessageFromReader(reader, conn, ControlReadTimeout)
			if err != nil {
				errCh <- err
				return
			}
			msgCh <- msg
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil

		case msg := <-msgCh:
			cmd, arg := ParseCommand(msg)
			switch cmd {
			case CmdPing:
				if err := SendMessage(conn, CmdPong); err != nil {
					return fmt.Errorf("failed to send pong: %w", err)
				}

			case CmdSessionRequest:
				sessionID := arg
				RelayLog("[relay-client] Session request received: session_id=%s", sessionID)
				go c.handleSessionRequest(sessionID)

			default:
				RelayLog("[relay-client] Unexpected control message: %q", msg)
			}

		case err := <-errCh:
			return fmt.Errorf("control connection read error: %w", err)
		}
	}
}

// handleSessionRequest opens a new data connection to the relay server for a session.
// The data connection is then handed to the torrent client via the sessionConns channel.
func (c *Client) handleSessionRequest(sessionID string) {
	RelayLog("[relay-client] Opening data connection for session %s to %s", sessionID, c.relayAddr)

	conn, err := net.DialTimeout("tcp", c.relayAddr, ConnectTimeout)
	if err != nil {
		RelayLog("[relay-client] Failed to open data connection for session %s: %v", sessionID, err)
		return
	}

	// Set TCP optimizations on data connection for better throughput
	optimizeTCPConn(conn)

	// Send session acceptance
	if err := SendMessage(conn, fmt.Sprintf("%s %s", CmdSession, sessionID)); err != nil {
		RelayLog("[relay-client] Failed to send session acceptance for %s: %v", sessionID, err)
		conn.Close()
		return
	}

	// Read OK response
	resp, err := ReadMessage(conn, 10*time.Second)
	if err != nil {
		RelayLog("[relay-client] Failed to read session response for %s: %v", sessionID, err)
		conn.Close()
		return
	}

	cmd, _ := ParseCommand(resp)
	if cmd != CmdOK {
		RelayLog("[relay-client] Session %s rejected: %s", sessionID, resp)
		conn.Close()
		return
	}

	RelayLog("[relay-client] Data connection established for session %s — handing to torrent client", sessionID)
	atomic.AddInt64(&c.sessionsHandled, 1)

	// Hand the connection to the torrent client's listener via channel.
	// The RelayListener.Accept() will pick this up.
	select {
	case c.sessionConns <- conn:
		RelayLog("[relay-client] Session %s connection queued for torrent client", sessionID)
	case <-time.After(10 * time.Second):
		RelayLog("[relay-client] WARNING: Session %s connection not accepted by torrent client (channel full), closing", sessionID)
		conn.Close()
	}
}

// AcceptChan returns the channel of relay session connections.
// Used by RelayListener to yield connections to the torrent client.
func (c *Client) AcceptChan() <-chan net.Conn {
	return c.sessionConns
}

// disconnect closes the control connection.
func (c *Client) disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.controlConn != nil {
		c.controlConn.Close()
		c.controlConn = nil
	}
	c.connected = false
}

// IsConnected returns whether the relay client has an active control connection.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// GetAdvertisedAddr returns the address this client is registered as.
func (c *Client) GetAdvertisedAddr() string {
	return c.advertisedAddr
}

// UpdateAdvertisedAddr updates the advertised address.
// The change takes effect on the next reconnect.
func (c *Client) UpdateAdvertisedAddr(addr string) {
	c.mu.Lock()
	c.advertisedAddr = addr
	c.mu.Unlock()
	RelayLog("[relay-client] Updated advertised address to %s (effective on next reconnect)", addr)
}

// GetStats returns relay client statistics.
func (c *Client) GetStats() (sessions int64, reconnects int64) {
	return atomic.LoadInt64(&c.sessionsHandled), atomic.LoadInt64(&c.reconnects)
}

// --- RelayListener ---

// RelayListener implements net.Listener by yielding connections from the relay client.
// It is added to the anacrolix/torrent client via AddListener() so the torrent client
// can accept incoming BitTorrent connections that arrive through the relay.
type RelayListener struct {
	client   *Client
	addr     net.Addr
	closed   chan struct{}
	closeOnce sync.Once
}

// NewRelayListener creates a listener that yields relay session connections.
func NewRelayListener(client *Client, relayAddr string) *RelayListener {
	return &RelayListener{
		client: client,
		addr:   &relayListenerAddr{relayAddr},
		closed: make(chan struct{}),
	}
}

// Accept waits for and returns the next relay session connection.
// Implements net.Listener.
func (l *RelayListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.client.AcceptChan():
		if conn == nil {
			return nil, fmt.Errorf("relay listener closed")
		}
		RelayLog("[relay-listener] Accepted relay session connection from %s", conn.RemoteAddr())
		return conn, nil
	case <-l.closed:
		return nil, fmt.Errorf("relay listener closed")
	}
}

// Close stops the listener.
func (l *RelayListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
	})
	return nil
}

// Addr returns the listener's network address.
func (l *RelayListener) Addr() net.Addr {
	return l.addr
}

// relayListenerAddr implements net.Addr for the relay listener.
type relayListenerAddr struct {
	addr string
}

func (a *relayListenerAddr) Network() string { return "tcp" }
func (a *relayListenerAddr) String() string  { return "relay:" + a.addr }
