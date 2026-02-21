package relay

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
	"time"
)

// NATStatus represents the NAT/firewall detection result for a server.
type NATStatus struct {
	IsBehindNAT bool   `json:"is_behind_nat"`
	ExternalIP  string `json:"external_ip"`
	LocalPort   int    `json:"local_port"`
	Reachable   bool   `json:"reachable"`
	LastChecked time.Time `json:"last_checked"`
}

// NATDetector performs NAT detection by asking the main server to probe
// whether this server's torrent data port is reachable from the outside.
type NATDetector struct {
	mainServerURL string
	serverID      string
	localPort     int

	mu     sync.RWMutex
	status NATStatus
}

// NewNATDetector creates a new NAT detector.
func NewNATDetector(mainServerURL, serverID string, localPort int) *NATDetector {
	return &NATDetector{
		mainServerURL: mainServerURL,
		serverID:      serverID,
		localPort:     localPort,
	}
}

// DetectOnce performs a single NAT detection check.
// Returns the updated NATStatus.
func (d *NATDetector) DetectOnce() NATStatus {
	RelayLog("[nat-detect] Checking port reachability: port=%d via main server %s",
		d.localPort, d.mainServerURL)

	// Ask main server to probe our port
	url := fmt.Sprintf("%s/api/v1/servers/%s/nat-check?port=%d",
		d.mainServerURL, d.serverID, d.localPort)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		RelayLog("[nat-detect] Failed to contact main server for NAT check: %v", err)
		// If we can't reach main server, assume we're behind NAT (fail closed)
		status := NATStatus{
			IsBehindNAT: true,
			LocalPort:   d.localPort,
			Reachable:   false,
			LastChecked: time.Now(),
		}
		d.mu.Lock()
		d.status = status
		d.mu.Unlock()
		RelayLog("[nat-detect] Cannot reach main server — assuming behind NAT (fail closed)")
		return status
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		RelayLog("[nat-detect] Failed to read NAT check response: %v", err)
		status := NATStatus{
			IsBehindNAT: true,
			LocalPort:   d.localPort,
			Reachable:   false,
			LastChecked: time.Now(),
		}
		d.mu.Lock()
		d.status = status
		d.mu.Unlock()
		return status
	}

	var result struct {
		Reachable  bool   `json:"reachable"`
		ExternalIP string `json:"external_ip"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		RelayLog("[nat-detect] Failed to parse NAT check response: %v (body: %s)", err, string(body))
		status := NATStatus{
			IsBehindNAT: true,
			LocalPort:   d.localPort,
			Reachable:   false,
			LastChecked: time.Now(),
		}
		d.mu.Lock()
		d.status = status
		d.mu.Unlock()
		return status
	}

	status := NATStatus{
		IsBehindNAT: !result.Reachable,
		ExternalIP:  result.ExternalIP,
		LocalPort:   d.localPort,
		Reachable:   result.Reachable,
		LastChecked: time.Now(),
	}

	d.mu.Lock()
	d.status = status
	d.mu.Unlock()

	if status.IsBehindNAT {
		RelayLog("[nat-detect] NAT DETECTED: port %d is NOT reachable from main server (external_ip=%s)",
			d.localPort, status.ExternalIP)
	} else {
		RelayLog("[nat-detect] Server is directly reachable — no relay needed (external_ip=%s port=%d)",
			status.ExternalIP, d.localPort)
	}

	return status
}

// GetStatus returns the current NAT detection status.
func (d *NATDetector) GetStatus() NATStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.status
}

// StartPeriodicCheck runs NAT detection at startup and then every interval.
// Does NOT block — runs in a goroutine.
func (d *NATDetector) StartPeriodicCheck(interval time.Duration, stopCh <-chan struct{}) {
	// Immediate first check
	d.DetectOnce()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				d.DetectOnce()
			}
		}
	}()
}

// --- Main server side: NAT check handler ---

// HandleNATCheck is an HTTP handler for the main server that probes whether
// a client's torrent data port is reachable. Called by client servers.
//
// GET /api/v1/servers/{id}/nat-check?port=10852
//
// The main server extracts the client's IP from the HTTP request and attempts
// a TCP connection to client_ip:port to determine reachability.
func HandleNATCheck(w http.ResponseWriter, r *http.Request) {
	portStr := r.URL.Query().Get("port")
	if portStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "missing port parameter",
		})
		return
	}

	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	if port <= 0 || port > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "invalid port",
		})
		return
	}

	// Get client's IP from the request
	clientIP := extractClientIP(r)
	if clientIP == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "could not determine client IP",
		})
		return
	}

	// Attempt TCP connection to client_ip:port
	addr := fmt.Sprintf("%s:%d", clientIP, port)
	RelayLog("[nat-detect] Probing %s for NAT check...", addr)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		RelayLog("[nat-detect] Probe FAILED for %s: %v (peer is behind NAT/firewall)", addr, err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"reachable":   false,
			"external_ip": clientIP,
			"error":       err.Error(),
		})
		return
	}
	conn.Close()

	RelayLog("[nat-detect] Probe SUCCESS for %s (peer is directly reachable)", addr)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"reachable":   true,
		"external_ip": clientIP,
	})
}

// extractClientIP gets the real client IP from the request, checking X-Forwarded-For.
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For first (for reverse proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := splitFirst(xff, ",")
		ip := trimSpace(parts)
		if ip != "" {
			return ip
		}
	}

	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return trimSpace(xri)
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func splitFirst(s, sep string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep[0] {
			return s[:i]
		}
	}
	return s
}

func trimSpace(s string) string {
	// Simple trim without importing strings
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
