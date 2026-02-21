package scanner

import (
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
)

// PeriodicScanner runs full scans on a schedule
type PeriodicScanner struct {
	scanPath     string
	interval     time.Duration
	database     *db.DB
	serverID     uuid.UUID
	indexer      *Indexer
	stopChan     chan struct{}
}

// NewPeriodicScanner creates a new periodic scanner. interval is how often to run a full scan (e.g. 12*time.Hour or 15*time.Minute).
func NewPeriodicScanner(scanPath string, interval time.Duration, database *db.DB, serverID uuid.UUID) *PeriodicScanner {
	return &PeriodicScanner{
		scanPath: scanPath,
		interval: interval,
		database: database,
		serverID: serverID,
		indexer:  NewIndexer(database, serverID),
		stopChan: make(chan struct{}),
	}
}

// GetIndexer returns the indexer for configuration
func (ps *PeriodicScanner) GetIndexer() *Indexer {
	return ps.indexer
}

// Start begins the periodic scanning
func (ps *PeriodicScanner) Start() {
	log.Printf("Periodic scanner started (interval: %v)", ps.interval)

	// Run initial full scan immediately
	go ps.RunFullScan()

	// Start periodic scanning
	go ps.scheduleScans()
}

// Stop stops the periodic scanner
func (ps *PeriodicScanner) Stop() {
	close(ps.stopChan)
	log.Println("Periodic scanner stopped")
}

// scheduleScans runs full scans on the configured interval
func (ps *PeriodicScanner) scheduleScans() {
	ticker := time.NewTicker(ps.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Println("Starting scheduled full scan...")
			ps.RunFullScan()

		case <-ps.stopChan:
			return
		}
	}
}

// RunFullScan performs a complete scan of the archive. Exported so clients can run it when the main server requests a rescan.
func (ps *PeriodicScanner) RunFullScan() {
	ps.runFullScan()
}

// runFullScan performs a complete scan of the archive
func (ps *PeriodicScanner) runFullScan() {
	startTime := time.Now()
	log.Printf("Starting full scan of: %s", ps.scanPath)

	// Create scan log entry
	scanLog := &db.ScanLog{
		ID:        uuid.New(),
		ServerID:  ps.serverID,
		ScanType:  "full_scan",
		StartedAt: startTime,
		Status:    "running",
	}

	if err := ps.database.CreateScanLog(scanLog); err != nil {
		log.Printf("Error creating scan log: %v", err)
	}

	// Discover all packages
	packages, err := DiscoverDCPPackages(ps.scanPath)
	if err != nil {
		log.Printf("Error discovering packages: %v", err)
		ps.updateScanLog(scanLog, err)
		return
	}

	scanLog.PackagesFound = len(packages)
	log.Printf("Discovered %d DCP packages", len(packages))

	// Scan and index each package
	added := 0
	updated := 0
	errors := 0

	for i, packagePath := range packages {
		log.Printf("Scanning package %d/%d: %s", i+1, len(packages), packagePath)

		info, err := ScanPackage(packagePath)
		if err != nil {
			log.Printf("Error scanning package %s: %v", packagePath, err)
			errors++
			continue
		}

		// Check if package exists
		assetMapUUID, parseErr := uuid.Parse(ExtractUUID(info.AssetMap.ID))
		if parseErr != nil {
			log.Printf("Error parsing UUID for %s: %v", packagePath, parseErr)
			errors++
			continue
		}

		existing, err := ps.database.GetDCPPackageByAssetMapUUID(assetMapUUID)
		if err != nil {
			log.Printf("Error checking existing package: %v", err)
			errors++
			continue
		}

		// Index the package
		if err := ps.indexer.IndexPackage(info); err != nil {
			log.Printf("Error indexing package %s: %v", packagePath, err)
			errors++
			continue
		}

		if existing == nil {
			added++
		} else {
			updated++
		}

		// Log progress every 10 packages
		if (i+1)%10 == 0 {
			log.Printf("Progress: %d/%d packages processed (added: %d, updated: %d, errors: %d)",
				i+1, len(packages), added, updated, errors)
		}
	}

	// Update scan log with results
	scanLog.PackagesAdded = added
	scanLog.PackagesUpdated = updated
	scanLog.Status = "success"
	if errors > 0 {
		scanLog.Status = "partial"
		scanLog.Errors = ""
	}

	duration := time.Since(startTime)
	log.Printf("Full scan complete: %d packages found, %d added, %d updated, %d errors (took %v)",
		scanLog.PackagesFound, added, updated, errors, duration)

	ps.updateScanLog(scanLog, nil)
}

// updateScanLog updates the scan log with final results
func (ps *PeriodicScanner) updateScanLog(scanLog *db.ScanLog, scanErr error) {
	now := time.Now()
	scanLog.CompletedAt = &now

	if scanErr != nil {
		scanLog.Status = "failed"
		scanLog.Errors = scanErr.Error()
	}

	if err := ps.database.UpdateScanLog(scanLog); err != nil {
		log.Printf("Error updating scan log: %v", err)
	}
}

// ExtractUUID is a helper function (duplicate from parser package to avoid import cycle)
func ExtractUUID(urn string) string {
	if len(urn) > 9 && urn[:9] == "urn:uuid:" {
		return urn[9:]
	}
	return urn
}
