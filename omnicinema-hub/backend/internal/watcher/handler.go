package watcher

import (
	"log"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/scanner"
)

// ScanHandler handles scan requests triggered by the watcher
type ScanHandler struct {
	database *db.DB
	serverID uuid.UUID
	indexer  *scanner.Indexer
}

// NewScanHandler creates a new scan handler
func NewScanHandler(database *db.DB, serverID uuid.UUID) *ScanHandler {
	return &ScanHandler{
		database: database,
		serverID: serverID,
		indexer:  scanner.NewIndexer(database, serverID),
	}
}

// GetIndexer returns the indexer for configuration
func (h *ScanHandler) GetIndexer() *scanner.Indexer {
	return h.indexer
}

// HandleScanRequest processes a scan request for a single package
func (h *ScanHandler) HandleScanRequest(packagePath string) error {
	log.Printf("Handling scan request for: %s", packagePath)

	// Scan the package
	info, err := scanner.ScanPackage(packagePath)
	if err != nil {
		return err
	}

	// Index to database
	if err := h.indexer.IndexPackage(info); err != nil {
		return err
	}

	log.Printf("Successfully processed scan request for: %s", packagePath)
	return nil
}

// StartScanWorker starts a worker goroutine that processes scan requests
func (h *ScanHandler) StartScanWorker(scanRequests <-chan string, stopChan <-chan struct{}) {
	log.Println("Starting scan worker...")

	for {
		select {
		case packagePath := <-scanRequests:
			if err := h.HandleScanRequest(packagePath); err != nil {
				log.Printf("Error processing scan request for %s: %v", packagePath, err)
			}

		case <-stopChan:
			log.Println("Scan worker stopped")
			return
		}
	}
}
