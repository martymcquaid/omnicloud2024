package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// LogEntry is a single log line sent to the main server
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
}

// LogForwarder sends logs to the main server
type LogForwarder struct {
	mainServerURL string
	serverID      string
	serverName    string
	macAddress    string
	
	buffer      []LogEntry
	bufferMu    sync.Mutex
	maxBuffer   int
	flushInterval time.Duration
	
	stopChan chan struct{}
	client   *http.Client
}

// LogBatch represents a batch of logs to send
type LogBatch struct {
	ServerID   string     `json:"server_id"`
	ServerName string     `json:"server_name"`
	Entries    []LogEntry `json:"entries"`
}

// NewLogForwarder creates a new log forwarder
func NewLogForwarder(mainServerURL, serverID, serverName, macAddress string) *LogForwarder {
	return &LogForwarder{
		mainServerURL: mainServerURL,
		serverID:      serverID,
		serverName:    serverName,
		macAddress:    macAddress,
		buffer:        make([]LogEntry, 0, 100),
		maxBuffer:     100,
		flushInterval: 5 * time.Second,
		stopChan:      make(chan struct{}),
		client:        &http.Client{Timeout: 10 * time.Second},
	}
}

// Start begins the log forwarder
func (lf *LogForwarder) Start() {
	log.Printf("Starting log forwarder to %s", lf.mainServerURL)
	
	// Create a custom log writer that captures logs
	go lf.periodicFlush()
}

// Stop stops the log forwarder
func (lf *LogForwarder) Stop() {
	close(lf.stopChan)
	// Final flush
	lf.flush()
}

// AddLog adds a log entry to the buffer
func (lf *LogForwarder) AddLog(level, message, source string) {
	lf.bufferMu.Lock()
	defer lf.bufferMu.Unlock()
	
	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Source:    source,
	}
	
	lf.buffer = append(lf.buffer, entry)
	
	// If buffer is full, flush
	if len(lf.buffer) >= lf.maxBuffer {
		go lf.flush()
	}
}

// periodicFlush periodically sends logs to main server
func (lf *LogForwarder) periodicFlush() {
	ticker := time.NewTicker(lf.flushInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			lf.flush()
		case <-lf.stopChan:
			return
		}
	}
}

// flush sends buffered logs to main server
func (lf *LogForwarder) flush() {
	lf.bufferMu.Lock()
	if len(lf.buffer) == 0 {
		lf.bufferMu.Unlock()
		return
	}
	
	// Copy and clear buffer
	entries := make([]LogEntry, len(lf.buffer))
	copy(entries, lf.buffer)
	lf.buffer = lf.buffer[:0]
	lf.bufferMu.Unlock()
	
	// Send to main server
	batch := LogBatch{
		ServerID:   lf.serverID,
		ServerName: lf.serverName,
		Entries:    entries,
	}
	
	jsonData, err := json.Marshal(batch)
	if err != nil {
		log.Printf("Failed to marshal log batch: %v", err)
		return
	}
	
	url := fmt.Sprintf("%s/api/v1/logs/ingest", lf.mainServerURL)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Failed to create log request: %v", err)
		return
	}
	
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", lf.serverID)
	req.Header.Set("X-MAC-Address", lf.macAddress)
	
	resp, err := lf.client.Do(req)
	if err != nil {
		// Don't log this as it creates recursion - just silently fail
		return
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		// Silently ignore errors to avoid recursion
		return
	}
}

// LogWriter implements io.Writer for capturing log output
type LogWriter struct {
	forwarder    *LogForwarder
	originalOut  io.Writer
	source       string
}

// NewLogWriter creates a writer that forwards logs
func NewLogWriter(forwarder *LogForwarder, originalOut io.Writer, source string) *LogWriter {
	return &LogWriter{
		forwarder:   forwarder,
		originalOut: originalOut,
		source:      source,
	}
}

// Write implements io.Writer
func (lw *LogWriter) Write(p []byte) (n int, err error) {
	// Write to original output
	n, err = lw.originalOut.Write(p)
	
	// Forward to main server
	if lw.forwarder != nil {
		message := string(p)
		// Determine log level from message
		level := "INFO"
		if bytes.Contains(p, []byte("ERROR")) || bytes.Contains(p, []byte("error")) {
			level = "ERROR"
		} else if bytes.Contains(p, []byte("WARNING")) || bytes.Contains(p, []byte("Warning")) {
			level = "WARNING"
		} else if bytes.Contains(p, []byte("DEBUG")) || bytes.Contains(p, []byte("debug")) {
			level = "DEBUG"
		}
		
		lw.forwarder.AddLog(level, message, lw.source)
	}
	
	return n, err
}

// SetupLogForwarding configures the standard logger to forward logs
func SetupLogForwarding(forwarder *LogForwarder) {
	// Create a multi-writer that writes to both stdout and the forwarder
	writer := NewLogWriter(forwarder, os.Stdout, "omnicloud")
	log.SetOutput(writer)
}
