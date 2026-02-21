package relay

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// relayLogger writes relay-specific log messages to both the main log and
// a dedicated relay log file. This makes it easy to debug relay issues on
// client servers by checking a single file.
var relayLogger struct {
	mu       sync.Mutex
	file     *os.File
	logger   *log.Logger
	initOnce sync.Once
	logDir   string
}

// InitRelayLog initializes the relay-specific log file.
// The log file is created at <logDir>/relay.log.
// All relay log messages (tagged with [relay-*]) are written to both
// the main log and this dedicated file.
// Safe to call multiple times; only the first call takes effect.
func InitRelayLog(logDir string) {
	relayLogger.initOnce.Do(func() {
		relayLogger.logDir = logDir

		logPath := filepath.Join(logDir, "relay.log")

		// Open log file with append mode
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("[relay] WARNING: Could not open relay log file %s: %v (relay logs will only go to main log)", logPath, err)
			return
		}

		relayLogger.file = f
		relayLogger.logger = log.New(f, "", 0) // No prefix, we format our own
		log.Printf("[relay] Relay log file initialized: %s", logPath)
	})
}

// RelayLog writes a log message to both the main log and the relay-specific log file.
// Format is the same as log.Printf.
func RelayLog(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)

	// Always write to main log
	log.Print(msg)

	// Also write to relay-specific log file if initialized
	relayLogger.mu.Lock()
	if relayLogger.logger != nil {
		timestamp := time.Now().Format("2006/01/02 15:04:05")
		relayLogger.logger.Printf("%s %s", timestamp, msg)
	}
	relayLogger.mu.Unlock()
}

// CloseRelayLog closes the relay log file.
func CloseRelayLog() {
	relayLogger.mu.Lock()
	defer relayLogger.mu.Unlock()
	if relayLogger.file != nil {
		relayLogger.file.Close()
		relayLogger.file = nil
		relayLogger.logger = nil
	}
}
