package scanner

import (
	"log"
	"path/filepath"
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

// NewPeriodicScanner creates a new periodic scanner
func NewPeriodicScanner(scanPath string, intervalHours int, database *db.DB, serverID uuid.UUID) *PeriodicScanner {
	return &PeriodicScanner{
		scanPath: scanPath,
		interval: time.Duration(intervalHours) * time.Hour,
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
	go ps.runFullScan()

	// Start periodic scanning
	go ps.scheduleScans()
}

// Stop stops the periodic scanner
func (ps *PeriodicScanner) Stop() {
	close(ps.stopChan)
	log.Println("Periodic scanner stopped")
}

// RunFullScan runs a full scan (exported for HTTP-triggered rescans)
func (ps *PeriodicScanner) RunFullScan() {
	ps.runFullScan()
}

// scheduleScans runs full scans on the configured interval
func (ps *PeriodicScanner) scheduleScans() {
	ticker := time.NewTicker(ps.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Println("Starting scheduled full scan...")
			ps.runFullScan()

		case <-ps.stopChan:
			return
		}
	}
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

		// Update scan log progress every 10 packages so API can report live status
		if (i+1)%10 == 0 {
			if err := ps.database.UpdateScanLogProgress(scanLog.ID, added, updated); err != nil {
				log.Printf("Error updating scan progress: %v", err)
			}
			log.Printf("Progress: %d/%d packages processed (added: %d, updated: %d, errors: %d)",
				i+1, len(packages), added, updated, errors)
		}
	}

	// Clean up packages that are no longer available on this server
	removed, err := ps.cleanupMissingPackages(packages)
	if err != nil {
		log.Printf("Error during inventory cleanup: %v", err)
		errors++
	}

	// Clean up torrent queue entries for packages no longer on this server
	queueRemoved, err := ps.cleanupOrphanedQueueEntries(packages)
	if err != nil {
		log.Printf("Error during queue cleanup: %v", err)
		errors++
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
	log.Printf("Full scan complete: %d packages found, %d added, %d updated, %d inventory removed, %d queue entries removed, %d errors (took %v)",
		scanLog.PackagesFound, added, updated, removed, queueRemoved, errors, duration)

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

// cleanupMissingPackages removes inventory entries for packages no longer on this server
// Keeps the package metadata and torrent, just removes this server's inventory reference
// If no servers have the content, it won't show in the UI
func (ps *PeriodicScanner) cleanupMissingPackages(currentPackages []string) (int, error) {
	// Get all packages currently in inventory for this server
	allInventory, err := ps.database.GetServerInventory(ps.serverID)
	if err != nil {
		log.Printf("Error getting server inventory: %v", err)
		return 0, err
	}

	// Create a map of currently available packages by path (normalize to match indexer)
	availablePackages := make(map[string]bool)
	for _, pkgPath := range currentPackages {
		availablePackages[filepath.Clean(pkgPath)] = true
	}

	removed := 0

	// Check each inventory entry (compare normalized paths)
	for _, inv := range allInventory {
		invPathNorm := filepath.Clean(inv.LocalPath)
		if !availablePackages[invPathNorm] {
			log.Printf("Package no longer available on this server: %s (inventory: %s)", inv.LocalPath, inv.PackageID)

			// Delete the inventory entry
			err := ps.database.DeleteServerDCPInventory(inv.ID)
			if err != nil {
				log.Printf("Error deleting inventory entry: %v", err)
				continue
			}

			removed++

			// Note: We keep the package metadata and torrent in the database
			// They'll only show in the UI if another server still has the content
			log.Printf("Removed inventory entry for package %s from this server", inv.PackageID)
		}
	}

	return removed, nil
}

// cleanupOrphanedQueueEntries removes torrent queue entries for packages no longer on this server
// This ensures the queue stays in sync with actual content availability
func (ps *PeriodicScanner) cleanupOrphanedQueueEntries(currentPackages []string) (int, error) {
	// Get all packages currently in inventory for this server
	allInventory, err := ps.database.GetServerInventory(ps.serverID)
	if err != nil {
		log.Printf("Error getting server inventory: %v", err)
		return 0, err
	}

	// Create a map of currently available packages by path (normalize to match indexer)
	availablePackages := make(map[string]bool)
	for _, pkgPath := range currentPackages {
		availablePackages[filepath.Clean(pkgPath)] = true
	}

	removed := 0

	// Check each inventory entry (compare normalized paths)
	for _, inv := range allInventory {
		invPathNorm := filepath.Clean(inv.LocalPath)
		if !availablePackages[invPathNorm] {
			log.Printf("Cleaning up torrent queue for missing package: %s", inv.PackageID)

			// Delete queue entries for this package on this server
			// Note: We directly access the underlying sql.DB since DB embeds *sql.DB
			query := `DELETE FROM torrent_queue WHERE package_id = $1 AND server_id = $2`
			result, err := ps.database.DB.Exec(query, inv.PackageID.String(), ps.serverID.String())
			if err != nil {
				log.Printf("Error deleting queue entries: %v", err)
				continue
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				log.Printf("Error getting rows affected: %v", err)
				continue
			}

			if rowsAffected > 0 {
				removed += int(rowsAffected)
				log.Printf("Removed %d queue entries for package %s", rowsAffected, inv.PackageID)
			}
		}
	}

	return removed, nil
}
