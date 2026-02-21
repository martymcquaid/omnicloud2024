package relay

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Protocol message types for the relay wire protocol.
// All messages are text-based (newline-delimited) for easy debugging.

const (
	// Commands sent by clients
	CmdRegister = "RELAY-REGISTER" // Seeder registers availability: "RELAY-REGISTER <ip:port>"
	CmdConnect  = "RELAY-CONNECT"  // Downloader requests bridge:    "RELAY-CONNECT <ip:port>"
	CmdSession  = "RELAY-SESSION"  // Seeder accepts session:        "RELAY-SESSION <session_id>"
	CmdPing     = "RELAY-PING"     // Keepalive ping
	CmdPong     = "RELAY-PONG"     // Keepalive pong

	// Commands sent by server
	CmdSessionRequest = "SESSION-REQUEST" // Server asks seeder to open data conn: "SESSION-REQUEST <session_id>"
	CmdOK             = "OK"              // Success response (may include data after space)
	CmdError          = "ERROR"           // Error response: "ERROR <reason>"

	// Timeouts
	ControlReadTimeout  = 90 * time.Second  // Control connection read timeout (must be > PingInterval)
	ControlWriteTimeout = 10 * time.Second  // Control connection write timeout
	SessionSetupTimeout = 30 * time.Second  // Time allowed for both sides to establish a session
	PingInterval        = 30 * time.Second  // Keepalive ping interval
	DataConnTimeout     = 15 * time.Second  // Timeout for seeder to open data connection after SESSION-REQUEST
	ConnectTimeout      = 10 * time.Second  // Timeout for relay dialer to connect to relay server

	// Defaults
	DefaultRelayPort    = 10866
	DefaultMaxSessions  = 100
)

// Session represents a relay session bridging two peers.
type Session struct {
	ID        string
	TargetAddr string    // The advertised ip:port of the seeder
	CreatedAt  time.Time

	// These are set when each side connects
	DownloaderConn net.Conn
	SeederConn     net.Conn
}

// NewSessionID generates a unique session identifier.
func NewSessionID() string {
	return uuid.New().String()[:8] // Short for readability in logs
}

// RegisteredPeer represents a seeder that has registered with the relay.
type RegisteredPeer struct {
	AdvertisedAddr string   // The ip:port this peer is known as in the tracker
	ControlConn    net.Conn // Persistent control connection from seeder
	RegisteredAt   time.Time
	LastPing       time.Time
}

// SendMessage writes a protocol message to a connection with a write deadline.
func SendMessage(conn net.Conn, msg string) error {
	conn.SetWriteDeadline(time.Now().Add(ControlWriteTimeout))
	_, err := fmt.Fprintf(conn, "%s\n", msg)
	return err
}

// ReadMessage reads a single newline-delimited message from a connection.
func ReadMessage(conn net.Conn, timeout time.Duration) (string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// ReadMessageFromReader reads a message from an existing buffered reader.
func ReadMessageFromReader(reader *bufio.Reader, conn net.Conn, timeout time.Duration) (string, error) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// ParseCommand splits a protocol message into command and argument.
// e.g. "RELAY-REGISTER 1.2.3.4:10852" → ("RELAY-REGISTER", "1.2.3.4:10852")
func ParseCommand(msg string) (cmd string, arg string) {
	parts := strings.SplitN(msg, " ", 2)
	cmd = parts[0]
	if len(parts) > 1 {
		arg = parts[1]
	}
	return
}
