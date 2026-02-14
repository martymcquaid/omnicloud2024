package watcher

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors filesystem changes
type Watcher struct {
	fsWatcher     *fsnotify.Watcher
	scanPath      string
	scanRequests  chan string
	debounceTime  time.Duration
	pendingEvents map[string]time.Time
	eventMutex    sync.Mutex
	stopChan      chan struct{}
}

// NewWatcher creates a new filesystem watcher
func NewWatcher(scanPath string, scanRequests chan string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	w := &Watcher{
		fsWatcher:     fsWatcher,
		scanPath:      scanPath,
		scanRequests:  scanRequests,
		debounceTime:  10 * time.Second, // Wait 10 seconds before triggering scan
		pendingEvents: make(map[string]time.Time),
		stopChan:      make(chan struct{}),
	}

	return w, nil
}

// Start begins watching the filesystem
func (w *Watcher) Start() error {
	// Add the root scan path to watch
	if err := w.fsWatcher.Add(w.scanPath); err != nil {
		return fmt.Errorf("failed to watch path %s: %w", w.scanPath, err)
	}

	log.Printf("Filesystem watcher started for: %s", w.scanPath)

	// Start event processing goroutine
	go w.processEvents()

	// Start debounce processor goroutine
	go w.processPendingEvents()

	return nil
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopChan)
	w.fsWatcher.Close()
	log.Println("Filesystem watcher stopped")
}

// processEvents handles filesystem events
func (w *Watcher) processEvents() {
	for {
		select {
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			log.Printf("Watcher error: %v", err)

		case <-w.stopChan:
			return
		}
	}
}

// handleEvent processes a single filesystem event
func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Only care about specific file types
	fileName := filepath.Base(event.Name)
	upperName := strings.ToUpper(fileName)

	// Check if this is a DCP-related file
	isDCPFile := upperName == "ASSETMAP" ||
		upperName == "ASSETMAP.XML" ||
		strings.HasPrefix(upperName, "CPL_") ||
		strings.HasPrefix(upperName, "PKL_") ||
		strings.HasSuffix(upperName, ".MXF")

	if !isDCPFile {
		return
	}

	// Get the package directory (parent of the changed file)
	packagePath := filepath.Dir(event.Name)

	// Log the event
	log.Printf("Detected change in DCP package: %s (event: %s, file: %s)",
		packagePath, event.Op.String(), fileName)

	// Add to pending events with debounce
	w.eventMutex.Lock()
	w.pendingEvents[packagePath] = time.Now()
	w.eventMutex.Unlock()

	// Handle REMOVE events immediately
	if event.Op&fsnotify.Remove == fsnotify.Remove {
		// TODO: Mark package as offline in database
		log.Printf("Package removed: %s", packagePath)
	}
}

// processPendingEvents checks for pending events and triggers scans after debounce period
func (w *Watcher) processPendingEvents() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.checkPendingEvents()
		case <-w.stopChan:
			return
		}
	}
}

// checkPendingEvents checks if any pending events are ready to trigger a scan
func (w *Watcher) checkPendingEvents() {
	now := time.Now()
	w.eventMutex.Lock()
	defer w.eventMutex.Unlock()

	var toScan []string
	for packagePath, eventTime := range w.pendingEvents {
		if now.Sub(eventTime) >= w.debounceTime {
			toScan = append(toScan, packagePath)
			delete(w.pendingEvents, packagePath)
		}
	}

	// Trigger scans for debounced packages
	for _, packagePath := range toScan {
		log.Printf("Triggering scan for changed package: %s", packagePath)
		select {
		case w.scanRequests <- packagePath:
			// Successfully sent
		default:
			log.Printf("Warning: scan request channel full, skipping %s", packagePath)
		}
	}
}
