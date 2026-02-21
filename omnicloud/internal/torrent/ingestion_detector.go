package torrent

import (
	"context"
	"database/sql"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/parser"
)

// IngestionSettingsProvider abstracts the settings methods needed by the ingestion detector.
// This avoids a direct import of the api package which would create an import cycle.
type IngestionSettingsProvider interface {
	GetRosettaBridgePath() (string, error)
	GetAutoCleanupAfterIngestion() (bool, error)
	ReportIngestion(packageID, infoHash, downloadPath, rbPath, status, errorMsg string) error
}

// IngestionDetector monitors for RosettaBridge ingestion of downloaded DCPs.
// It periodically checks if a downloaded DCP has appeared in the RosettaBridge
// library location, verifies file integrity, switches torrent seeding to the
// new location, and optionally cleans up the original download.
//
// Matching strategy: Uses CPL UUID as the canonical link between the downloaded
// DCP and the ingested copy. RosettaBridge rewrites ASSETMAP UUID, PKL UUID,
// and file names during ingest, but the CPL UUID (Composition Playlist ID)
// stays the same. See Ingestprocess.md for details.
type IngestionDetector struct {
	client         *Client
	db             *sql.DB
	serverID       string
	settingsClient IngestionSettingsProvider
	checkInterval  time.Duration
}

// NewIngestionDetector creates a new ingestion detector
func NewIngestionDetector(client *Client, db *sql.DB, serverID string, settingsClient IngestionSettingsProvider) *IngestionDetector {
	return &IngestionDetector{
		client:         client,
		db:             db,
		serverID:       serverID,
		settingsClient: settingsClient,
		checkInterval:  60 * time.Second,
	}
}

// Start begins the ingestion detection loop. It runs until the context is cancelled.
func (d *IngestionDetector) Start(ctx context.Context) {
	log.Println("[ingestion] Starting RosettaBridge ingestion detector (checks every 60s, matches by CPL UUID)...")

	// Initial delay to let downloads get started
	time.Sleep(30 * time.Second)

	ticker := time.NewTicker(d.checkInterval)
	defer ticker.Stop()

	for {
		d.check()

		select {
		case <-ctx.Done():
			log.Println("[ingestion] Detector stopped")
			return
		case <-ticker.C:
		}
	}
}

// check performs a single ingestion detection cycle
func (d *IngestionDetector) check() {
	// Get RosettaBridge path from settings
	rbPath, err := d.settingsClient.GetRosettaBridgePath()
	if err != nil {
		log.Printf("[ingestion] Error fetching RosettaBridge path: %v", err)
		return
	}

	if rbPath == "" {
		return
	}

	// Get pending ingestion records (status = 'downloaded')
	rows, err := d.db.Query(`
		SELECT id, package_id, info_hash, download_path
		FROM dcp_ingestion_status
		WHERE server_id = $1 AND status = 'downloaded'
	`, d.serverID)
	if err != nil {
		log.Printf("[ingestion] Error querying ingestion records: %v", err)
		return
	}
	defer rows.Close()

	type pendingRecord struct {
		id           string
		packageID    string
		infoHash     string
		downloadPath string
	}

	var pending []pendingRecord
	for rows.Next() {
		var rec pendingRecord
		var id, pid uuid.UUID
		if err := rows.Scan(&id, &pid, &rec.infoHash, &rec.downloadPath); err != nil {
			log.Printf("[ingestion] Error scanning ingestion record: %v", err)
			continue
		}
		rec.id = id.String()
		rec.packageID = pid.String()
		pending = append(pending, rec)
	}

	if len(pending) == 0 {
		return
	}

	// Discover DCPs in the RosettaBridge path
	rbPackages, err := discoverDCPPackages(rbPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[ingestion] Error scanning RosettaBridge path %s: %v", rbPath, err)
		}
		return
	}

	if len(rbPackages) == 0 {
		return
	}

	// Build a map of CPL UUID -> RosettaBridge package path
	// RosettaBridge rewrites ASSETMAP UUID and PKL UUID on ingest,
	// but the CPL UUID remains the same - it's the canonical link.
	rbCPLMap := make(map[string]string) // cpl_uuid -> package_path
	for _, pkgPath := range rbPackages {
		cplUUIDs := extractCPLUUIDs(pkgPath)
		for _, cplUUID := range cplUUIDs {
			rbCPLMap[cplUUID] = pkgPath
		}
	}

	if len(rbCPLMap) == 0 {
		return
	}

	// For each pending download, check if any of its CPL UUIDs appear in RosettaBridge
	for _, rec := range pending {
		// Look up CPL UUIDs for this package from the database
		cplRows, err := d.db.Query(`
			SELECT cpl_uuid FROM dcp_compositions WHERE package_id = $1
		`, rec.packageID)
		if err != nil {
			log.Printf("[ingestion] Error looking up CPLs for package %s: %v", rec.packageID, err)
			continue
		}

		var matchedRBPath string
		var matchedCPL string
		for cplRows.Next() {
			var cplUUID string
			if err := cplRows.Scan(&cplUUID); err != nil {
				continue
			}
			if rbPath, found := rbCPLMap[cplUUID]; found {
				matchedRBPath = rbPath
				matchedCPL = cplUUID
				break
			}
		}
		cplRows.Close()

		if matchedRBPath == "" {
			continue
		}

		log.Printf("[ingestion] DETECTED: Package %s matched by CPL UUID %s in RosettaBridge at %s",
			rec.packageID, matchedCPL[:12], matchedRBPath)

		// Update status to 'detected'
		d.updateIngestionStatus(rec.id, "detected", matchedRBPath, "")

		// Verify file integrity using size-based matching
		// RosettaBridge renames files (MXFs become UUID-based names), so we compare
		// by total size and file count rather than individual filenames.
		ok, verifyErr := d.verifyIngestion(rec.downloadPath, matchedRBPath)
		if verifyErr != nil {
			log.Printf("[ingestion] Verification error for %s: %v", rec.packageID, verifyErr)
			d.updateIngestionStatus(rec.id, "failed", matchedRBPath, verifyErr.Error())
			d.reportToMainServer(rec.packageID, rec.infoHash, rec.downloadPath, matchedRBPath, "failed", verifyErr.Error())
			continue
		}
		if !ok {
			log.Printf("[ingestion] Verification FAILED for %s - content sizes don't match", rec.packageID)
			d.updateIngestionStatus(rec.id, "failed", matchedRBPath, "Content verification failed - total sizes don't match")
			d.reportToMainServer(rec.packageID, rec.infoHash, rec.downloadPath, matchedRBPath, "failed", "Content verification failed")
			continue
		}

		log.Printf("[ingestion] Verification PASSED for %s - content verified via CPL UUID + size match", rec.packageID)
		d.updateIngestionStatus(rec.id, "verified", matchedRBPath, "")

		// Switch torrent seeding to the RosettaBridge path
		if err := d.switchSeeding(rec.infoHash, matchedRBPath); err != nil {
			log.Printf("[ingestion] Seeding switch error for %s: %v", rec.packageID, err)
			d.updateIngestionStatus(rec.id, "failed", matchedRBPath, "Seeding switch failed: "+err.Error())
			d.reportToMainServer(rec.packageID, rec.infoHash, rec.downloadPath, matchedRBPath, "failed", "Seeding switch failed: "+err.Error())
			continue
		}

		log.Printf("[ingestion] Seeding switched for %s to %s", rec.packageID, matchedRBPath)
		d.updateIngestionStatus(rec.id, "seeding_switched", matchedRBPath, "")
		d.reportToMainServer(rec.packageID, rec.infoHash, rec.downloadPath, matchedRBPath, "seeding_switched", "")

		// Optional cleanup
		d.maybeCleanup(rec.id, rec.packageID, rec.downloadPath, matchedRBPath, rec.infoHash)
	}
}

// extractCPLUUIDs finds and parses all CPL files in a DCP package directory,
// returning their UUIDs. This works for both pre-ingest and post-ingest DCPs
// because the CPL UUID is preserved by RosettaBridge.
func extractCPLUUIDs(packagePath string) []string {
	cplPaths, err := findCPLFiles(packagePath)
	if err != nil || len(cplPaths) == 0 {
		return nil
	}

	var uuids []string
	for _, cplPath := range cplPaths {
		cpl, err := parser.ParseCPL(cplPath)
		if err != nil {
			log.Printf("[ingestion] Error parsing CPL at %s: %v", cplPath, err)
			continue
		}
		cplUUID := parser.ExtractUUID(cpl.ID)
		if cplUUID != "" {
			uuids = append(uuids, cplUUID)
		}
	}

	return uuids
}

// verifyIngestion verifies that the RosettaBridge copy contains all the content
// from the original download. Since RosettaBridge renames files (MXF files become
// UUID-based names like ee0b0030-59bc-4b1c-b162-de169378b08f.mxf), we cannot
// match by filename. Instead we verify by:
// 1. MXF file count matches (allow RB to have additional files like PKL.xml, VOLINDEX.xml)
// 2. Total MXF size matches (the actual content data)
// 3. CPL UUID match was already confirmed by the caller
func (d *IngestionDetector) verifyIngestion(downloadPath, rbPath string) (bool, error) {
	log.Printf("[ingestion] Verifying content: download=%s rosettabridge=%s", downloadPath, rbPath)

	// Get file sizes from both locations
	dlFiles, err := listFilesWithSizes(downloadPath)
	if err != nil {
		return false, err
	}

	rbFiles, err := listFilesWithSizes(rbPath)
	if err != nil {
		return false, err
	}

	if len(dlFiles) == 0 {
		return false, nil
	}

	// Count MXF files and total MXF size in both locations
	// MXF files are the actual content (picture, sound) and their sizes don't change
	dlMXFCount, dlMXFSize := countMXFFiles(dlFiles)
	rbMXFCount, rbMXFSize := countMXFFiles(rbFiles)

	log.Printf("[ingestion]   Download: %d total files, %d MXF files, %d bytes MXF data",
		len(dlFiles), dlMXFCount, dlMXFSize)
	log.Printf("[ingestion]   RosettaBridge: %d total files, %d MXF files, %d bytes MXF data",
		len(rbFiles), rbMXFCount, rbMXFSize)

	// Verify: MXF count must match (same content assets)
	if dlMXFCount != rbMXFCount {
		log.Printf("[ingestion]   MISMATCH: MXF file count differs (download=%d, rb=%d)", dlMXFCount, rbMXFCount)
		return false, nil
	}

	// Verify: total MXF size must match (content data integrity)
	if dlMXFSize != rbMXFSize {
		log.Printf("[ingestion]   MISMATCH: MXF total size differs (download=%d, rb=%d)", dlMXFSize, rbMXFSize)
		return false, nil
	}

	if dlMXFCount == 0 {
		log.Printf("[ingestion]   WARNING: no MXF files found in download - checking total file count instead")
		// Fallback: at least verify file counts are reasonable
		if len(rbFiles) < len(dlFiles)-2 { // Allow for renamed XML files
			return false, nil
		}
	}

	log.Printf("[ingestion]   VERIFIED: %d MXF files, %d bytes match", dlMXFCount, dlMXFSize)
	return true, nil
}

// countMXFFiles counts MXF files and their total size from a file map
func countMXFFiles(files map[string]int64) (int, int64) {
	count := 0
	var totalSize int64
	for name, size := range files {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".mxf") {
			count++
			totalSize += size
		}
	}
	return count, totalSize
}

// listFilesWithSizes walks a directory and returns a map of relative_path -> file_size
func listFilesWithSizes(dirPath string) (map[string]int64, error) {
	result := make(map[string]int64)

	entries, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			subPath := filepath.Join(dirPath, entry.Name())
			subFiles, err := listFilesWithSizes(subPath)
			if err != nil {
				continue
			}
			for subRel, size := range subFiles {
				result[filepath.Join(entry.Name(), subRel)] = size
			}
		} else {
			result[entry.Name()] = entry.Size()
		}
	}

	return result, nil
}

// switchSeeding drops the current torrent and re-adds it pointing to the RosettaBridge path
func (d *IngestionDetector) switchSeeding(infoHash, newPath string) error {
	var torrentID string
	err := d.db.QueryRow(`
		SELECT id FROM dcp_torrents WHERE info_hash = $1
	`, infoHash).Scan(&torrentID)
	if err != nil {
		return err
	}

	return d.client.SwitchSeedingPath(infoHash, newPath, torrentID)
}

// maybeCleanup removes the original download copy if auto_cleanup is enabled
func (d *IngestionDetector) maybeCleanup(recordID, packageID, downloadPath, rbPath, infoHash string) {
	autoCleanup, err := d.settingsClient.GetAutoCleanupAfterIngestion()
	if err != nil {
		log.Printf("[ingestion] Error checking auto-cleanup setting: %v", err)
		return
	}

	if !autoCleanup {
		log.Printf("[ingestion] Auto-cleanup disabled, keeping original at %s", downloadPath)
		return
	}

	dlClean := filepath.Clean(downloadPath)
	rbClean := filepath.Clean(rbPath)
	if dlClean == rbClean || strings.HasPrefix(dlClean, rbClean+string(os.PathSeparator)) {
		log.Printf("[ingestion] WARNING: download path %s is inside RosettaBridge path %s, skipping cleanup", dlClean, rbClean)
		return
	}

	log.Printf("[ingestion] Auto-cleanup: removing original download at %s", downloadPath)
	if err := os.RemoveAll(downloadPath); err != nil {
		log.Printf("[ingestion] Error cleaning up %s: %v", downloadPath, err)
		d.updateIngestionStatus(recordID, "seeding_switched", rbPath, "Cleanup failed: "+err.Error())
		return
	}

	log.Printf("[ingestion] Cleanup complete: removed %s", downloadPath)
	d.updateIngestionStatus(recordID, "cleanup_done", rbPath, "")
	d.reportToMainServer(packageID, infoHash, downloadPath, rbPath, "cleanup_done", "")
}

// updateIngestionStatus updates the status of an ingestion record in the local database
func (d *IngestionDetector) updateIngestionStatus(recordID, status, rbPath, errMsg string) {
	now := time.Now()

	var verifiedAt, switchedAt, cleanedAt *time.Time
	switch status {
	case "verified":
		verifiedAt = &now
	case "seeding_switched":
		switchedAt = &now
	case "cleanup_done":
		cleanedAt = &now
	}

	_, err := d.db.Exec(`
		UPDATE dcp_ingestion_status
		SET status = $1,
		    rosettabridge_path = $2,
		    error_message = $3,
		    verified_at = COALESCE($4, verified_at),
		    switched_at = COALESCE($5, switched_at),
		    cleaned_at = COALESCE($6, cleaned_at),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $7
	`, status, rbPath, errMsg, verifiedAt, switchedAt, cleanedAt, recordID)

	if err != nil {
		log.Printf("[ingestion] Error updating ingestion status for %s: %v", recordID, err)
	}
}

// reportToMainServer sends ingestion status to the main server
func (d *IngestionDetector) reportToMainServer(packageID, infoHash, downloadPath, rbPath, status, errMsg string) {
	if d.settingsClient == nil {
		return
	}

	if err := d.settingsClient.ReportIngestion(packageID, infoHash, downloadPath, rbPath, status, errMsg); err != nil {
		log.Printf("[ingestion] Error reporting to main server: %v", err)
	}
}

// discoverDCPPackages walks a directory and finds DCP packages (dirs containing ASSETMAP)
// Inlined from scanner package to avoid import cycle.
func discoverDCPPackages(rootPath string) ([]string, error) {
	var packages []string
	seen := make(map[string]bool)

	if _, err := os.Stat(rootPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("scan path does not exist: %s", rootPath)
	}

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		fileName := strings.ToUpper(info.Name())
		if fileName == "ASSETMAP" || fileName == "ASSETMAP.XML" {
			packagePath := filepath.Dir(path)
			if !seen[packagePath] {
				seen[packagePath] = true
				packages = append(packages, packagePath)
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking directory tree: %w", err)
	}

	return packages, nil
}

// findCPLFiles finds all CPL XML files in a package directory.
// Inlined from scanner package to avoid import cycle.
func findCPLFiles(packagePath string) ([]string, error) {
	var cplFiles []string

	entries, err := ioutil.ReadDir(packagePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read package directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		upperName := strings.ToUpper(name)

		if strings.HasPrefix(upperName, "CPL_") && strings.HasSuffix(upperName, ".XML") {
			cplFiles = append(cplFiles, filepath.Join(packagePath, name))
		}
		if strings.HasSuffix(strings.ToLower(name), ".cpl") {
			cplFiles = append(cplFiles, filepath.Join(packagePath, name))
		}
		// Also check for simply "CPL.xml" (RosettaBridge renames to this)
		if upperName == "CPL.XML" {
			cplFiles = append(cplFiles, filepath.Join(packagePath, name))
		}
	}

	return cplFiles, nil
}
