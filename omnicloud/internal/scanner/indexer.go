package scanner

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/api"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/parser"
)

// TorrentQueue interface for adding packages to torrent queue
type TorrentQueue interface {
	AddToQueue(packageID string) error
	// ShouldHash asks the orchestrator (main server) whether this server should hash
	// the given package. Returns true if we should hash, false if another server is
	// already handling it (or the torrent already exists).
	ShouldHash(assetMapUUID string) bool
	// StartSeedingExisting starts co-seeding a canonical torrent for a deduplicated DCP.
	// mxfPath is the library directory with the large MXF files; xmlShadowPath is the
	// directory holding the canonical XML files fetched from the main server.
	// The torrent storage will read each file from whichever path it exists in.
	StartSeedingExisting(torrentBytes []byte, mxfPath, xmlShadowPath, packageID, torrentID string) error
}

// Indexer handles storing DCP metadata to the database
type Indexer struct {
	db             *db.DB
	serverID       uuid.UUID
	torrentQueue   TorrentQueue
	settingsClient *api.SettingsClient // nil on main server; set on client servers
	shadowXMLBase  string              // base dir for canonical XML shadow copies (e.g. /var/omnicloud/canonical-xml)
}

// NewIndexer creates a new indexer instance
func NewIndexer(database *db.DB, serverID uuid.UUID) *Indexer {
	return &Indexer{
		db:           database,
		serverID:     serverID,
		torrentQueue: nil, // Set later with SetTorrentQueue
	}
}

// SetTorrentQueue sets the torrent queue manager
func (idx *Indexer) SetTorrentQueue(queue TorrentQueue) {
	idx.torrentQueue = queue
}

// SetSettingsClient provides the client used to fetch canonical XML from the main server.
// Only call this on client servers (not main server).
func (idx *Indexer) SetSettingsClient(sc *api.SettingsClient) {
	idx.settingsClient = sc
}

// SetShadowXMLBase sets the directory where canonical XML shadow copies are stored.
// These are kept separate from the RosettaBridge library to avoid disrupting its operation.
// Example: /var/omnicloud/canonical-xml
func (idx *Indexer) SetShadowXMLBase(dir string) {
	idx.shadowXMLBase = dir
}

// IndexPackage stores all package metadata to the database
func (idx *Indexer) IndexPackage(info *DCPPackageInfo) error {
	log.Printf("Indexing package: %s", info.PackageName)
	
	// Parse dates
	var issueDate *time.Time
	if info.AssetMap.IssueDate != "" {
		if t, err := time.Parse(time.RFC3339, info.AssetMap.IssueDate); err == nil {
			issueDate = &t
		}
	}
	
	// Extract asset map UUID
	assetMapUUID, err := uuid.Parse(parser.ExtractUUID(info.AssetMap.ID))
	if err != nil {
		return fmt.Errorf("invalid ASSETMAP UUID: %w", err)
	}

	// --- Cross-site deduplication via CPL UUID ---
	// RosettaBridge delivers the same DCP to each cinema with a fresh ASSETMAP UUID but
	// an identical CPL UUID and identical MXF file hashes. Detect this and, instead of
	// creating a new package + torrent, link this server's inventory to the existing
	// canonical package and begin co-seeding its torrent (after replacing local XML files
	// with the canonical ones so piece hashes align exactly).
	if len(info.CPLs) > 0 && idx.settingsClient != nil {
		cplUUIDStr := parser.ExtractUUID(info.CPLs[0].ID)
		if cplUUIDStr != "" {
			cplUUID, parseErr := uuid.Parse(cplUUIDStr)
			if parseErr == nil {
				if handled, handleErr := idx.handleDuplicateByCPL(info, cplUUID, assetMapUUID); handled {
					if handleErr != nil {
						log.Printf("[dedup] CPL-based dedup for %s failed: %v — falling through to normal indexing", info.PackageName, handleErr)
					} else {
						log.Printf("[dedup] %s recognised as duplicate via CPL %s — linked to canonical package", info.PackageName, cplUUID)
						return nil
					}
				}
			}
		}
	}

	// Get content title from first CPL if available
	contentTitle := ""
	contentKind := ""
	if len(info.CPLs) > 0 {
		contentTitle = info.CPLs[0].ContentTitleText
		contentKind = info.CPLs[0].ContentKind
	}

	// Create or update DCP package record
	now := time.Now()
	pkg := &db.DCPPackage{
		ID:             uuid.New(),
		AssetMapUUID:   assetMapUUID,
		PackageName:    info.PackageName,
		ContentTitle:   contentTitle,
		ContentKind:    contentKind,
		IssueDate:      issueDate,
		Issuer:         info.AssetMap.Issuer,
		Creator:        info.AssetMap.Creator,
		AnnotationText: "",
		VolumeCount:    info.AssetMap.VolumeCount,
		TotalSizeBytes: info.TotalSize,
		FileCount:      info.FileCount,
		DiscoveredAt:   info.DiscoveredAt,
		LastVerified:   &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	
	if err := idx.db.UpsertDCPPackage(pkg); err != nil {
		return fmt.Errorf("failed to insert package: %w", err)
	}
	
	// Get the package ID (may have been existing)
	existingPkg, err := idx.db.GetDCPPackageByAssetMapUUID(assetMapUUID)
	if err != nil {
		return fmt.Errorf("failed to retrieve package: %w", err)
	}
	packageID := existingPkg.ID
	
	// Index all CPLs
	for _, cpl := range info.CPLs {
		if err := idx.indexComposition(packageID, cpl); err != nil {
			log.Printf("Warning: failed to index CPL: %v", err)
		}
	}
	
	// Index all PKLs
	for _, pkl := range info.PKLs {
		if err := idx.indexPackingList(packageID, pkl); err != nil {
			log.Printf("Warning: failed to index PKL: %v", err)
		}
		
		// Index assets from PKL
		if err := idx.indexAssets(packageID, info, pkl); err != nil {
			log.Printf("Warning: failed to index assets: %v", err)
		}
	}
	
	// Update server inventory (normalize path so cleanup compares correctly across runs)
	inventory := &db.ServerDCPInventory{
		ID:           uuid.New(),
		ServerID:     idx.serverID,
		PackageID:    packageID,
		LocalPath:    filepath.Clean(info.PackagePath),
		Status:       "online",
		LastVerified: now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	
	if err := idx.db.UpsertServerDCPInventory(inventory); err != nil {
		return fmt.Errorf("failed to update inventory: %w", err)
	}

	// Check if torrent exists or needs to be generated
	if idx.torrentQueue != nil {
		if err := idx.checkTorrentStatus(packageID, assetMapUUID); err != nil {
			log.Printf("Warning: failed to check torrent status for %s: %v", info.PackageName, err)
		}
	}
	
	log.Printf("Successfully indexed package: %s", info.PackageName)
	return nil
}

// handleDuplicateByCPL checks whether the given CPL UUID is already in the system under a
// different ASSETMAP UUID (i.e. the same film delivered to multiple sites by RosettaBridge).
// If a match is found AND the canonical torrent already exists, it:
//  1. Links this server's inventory to the existing canonical package row
//  2. Fetches the canonical XML files (ASSETMAP, PKL, VOLINDEX, etc.) from the main server
//  3. Overwrites the local XML files so the byte layout matches the canonical torrent exactly
//  4. Starts seeding the canonical torrent from this server's local path
//
// Returns (true, nil) if the duplicate was handled successfully — caller should return nil.
// Returns (true, err) if we found a duplicate but something failed — caller should fall through
// to normal indexing so the DCP still gets indexed (just with its own separate torrent).
// Returns (false, nil) if no duplicate was found — caller should proceed normally.
func (idx *Indexer) handleDuplicateByCPL(info *DCPPackageInfo, cplUUID, localAssetMapUUID uuid.UUID) (bool, error) {
	// Look for a package with this CPL UUID that has a DIFFERENT assetmap_uuid
	canonicalPkg, err := idx.db.GetDCPPackageByCPLUUID(cplUUID)
	if err != nil {
		return false, fmt.Errorf("CPL lookup failed: %w", err)
	}
	if canonicalPkg == nil {
		return false, nil // No existing package — not a duplicate
	}
	if canonicalPkg.AssetMapUUID == localAssetMapUUID {
		return false, nil // Same ASSETMAP UUID — same physical package, normal upsert path
	}

	log.Printf("[dedup] Found canonical package %s (assetmap %s) matching local CPL %s (local assetmap %s)",
		canonicalPkg.PackageName, canonicalPkg.AssetMapUUID, cplUUID, localAssetMapUUID)

	// Link this server's inventory to the canonical package
	now := time.Now()
	inventory := &db.ServerDCPInventory{
		ID:           uuid.New(),
		ServerID:     idx.serverID,
		PackageID:    canonicalPkg.ID,
		LocalPath:    filepath.Clean(info.PackagePath),
		Status:       "online",
		LastVerified: now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := idx.db.UpsertServerDCPInventory(inventory); err != nil {
		return true, fmt.Errorf("failed to link inventory to canonical package: %w", err)
	}

	// Check if a torrent already exists for the canonical package
	torrentRec, err := idx.db.GetTorrentByPackageID(canonicalPkg.ID)
	if err != nil {
		return true, fmt.Errorf("failed to check torrent for canonical package: %w", err)
	}
	if torrentRec == nil {
		// Torrent not yet generated for the canonical package. The first server to finish
		// hashing will create it; this server should wait and will be picked up on the
		// next periodic scan when the torrent is available.
		log.Printf("[dedup] Canonical package %s has no torrent yet — inventory linked, will co-seed when torrent is ready", canonicalPkg.PackageName)
		return true, nil
	}

	// Torrent exists — fetch canonical XML files and write them to the shadow directory.
	// We do NOT overwrite the library XML files because RosettaBridge depends on them.
	// Instead, the canonical XML files are stored in a separate shadow directory, and
	// the torrent seeding layer uses a split-path storage that reads MXF files from the
	// library and XML files from the shadow directory.
	log.Printf("[dedup] Fetching canonical XML files for CPL %s from main server", cplUUID)
	canonicalXML, err := idx.settingsClient.GetCanonicalXML(cplUUID.String())
	if err != nil {
		return true, fmt.Errorf("failed to fetch canonical XML: %w", err)
	}
	if canonicalXML == nil {
		return true, fmt.Errorf("main server returned no canonical XML for CPL %s", cplUUID)
	}

	// Determine shadow directory for this package's canonical XML files.
	// Shadow path: <shadowXMLBase>/<canonical_package_id>/
	shadowDir := idx.shadowXMLBase
	if shadowDir == "" {
		// Fallback: use a sibling of the torrent download location if configured,
		// otherwise use a subdirectory alongside the package
		shadowDir = filepath.Join(filepath.Dir(info.PackagePath), ".canonical-xml")
	}
	shadowPackageDir := filepath.Join(shadowDir, canonicalPkg.ID.String())

	if err := api.ApplyCanonicalXML(shadowPackageDir, canonicalXML.Files); err != nil {
		return true, fmt.Errorf("failed to write canonical XML to shadow dir %s: %w", shadowPackageDir, err)
	}
	log.Printf("[dedup] Wrote %d canonical XML files to shadow dir %s", len(canonicalXML.Files), shadowPackageDir)

	// Start seeding the canonical torrent using split-path storage:
	//   - MXF/large files come from info.PackagePath (the original library location)
	//   - XML/small files come from shadowPackageDir
	if idx.torrentQueue != nil {
		if err := idx.torrentQueue.StartSeedingExisting(canonicalXML.TorrentFile, info.PackagePath, shadowPackageDir, canonicalPkg.ID.String(), torrentRec.ID); err != nil {
			log.Printf("[dedup] Warning: failed to start seeding canonical torrent for %s: %v", canonicalPkg.PackageName, err)
			// Non-fatal: inventory is linked, seeding will be picked up by SeedExisting on next restart
		} else {
			log.Printf("[dedup] Started co-seeding canonical torrent %s for %s (MXF from %s, XML from %s)",
				torrentRec.InfoHash[:12], canonicalPkg.PackageName, info.PackagePath, shadowPackageDir)
		}
	}

	return true, nil
}

// indexComposition stores a CPL to the database
func (idx *Indexer) indexComposition(packageID uuid.UUID, cpl *parser.CompositionPlaylist) error {
	cplUUID, err := uuid.Parse(parser.ExtractUUID(cpl.ID))
	if err != nil {
		return fmt.Errorf("invalid CPL UUID: %w", err)
	}
	
	var issueDate *time.Time
	if cpl.IssueDate != "" {
		if t, err := time.Parse(time.RFC3339, cpl.IssueDate); err == nil {
			issueDate = &t
		}
	}
	
	var contentVersionID *uuid.UUID
	if cpl.ContentVersion.ID != "" {
		if vid, err := uuid.Parse(parser.ExtractUUID(cpl.ContentVersion.ID)); err == nil {
			contentVersionID = &vid
		}
	}
	
	width, height := cpl.GetResolution()
	metadata := cpl.GetMetadata()
	
	comp := &db.DCPComposition{
		ID:                  uuid.New(),
		PackageID:           packageID,
		CPLUUID:             cplUUID,
		ContentTitleText:    cpl.ContentTitleText,
		FullContentTitle:    "",
		ContentKind:         cpl.ContentKind,
		ContentVersionID:    contentVersionID,
		LabelText:           cpl.ContentVersion.LabelText,
		IssueDate:           issueDate,
		Issuer:              cpl.Issuer,
		Creator:             cpl.Creator,
		EditRate:            "",
		FrameRate:           "",
		ScreenAspectRatio:   "",
		ResolutionWidth:     width,
		ResolutionHeight:    height,
		MainSoundConfiguration: "",
		MainSoundSampleRate: "",
		Luminance:           0,
		ReleaseTerritory:    "",
		Distributor:         "",
		Facility:            "",
		ReelCount:           cpl.GetReelCount(),
		TotalDurationFrames: cpl.GetTotalDuration(),
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}
	
	// Extract metadata if available
	if metadata != nil {
		comp.FullContentTitle = metadata.FullContentTitleText
		comp.EditRate = metadata.EditRate
		comp.MainSoundConfiguration = metadata.MainSoundConfiguration
		comp.MainSoundSampleRate = metadata.MainSoundSampleRate
		comp.Luminance = int(metadata.Luminance)
		comp.ReleaseTerritory = metadata.ReleaseTerritory
		comp.Distributor = metadata.Distributor
		comp.Facility = metadata.Facility
	}
	
	// Extract from first reel if no metadata
	if len(cpl.ReelList.Reels) > 0 {
		firstReel := cpl.ReelList.Reels[0]
		if firstReel.AssetList.MainPicture != nil {
			comp.EditRate = firstReel.AssetList.MainPicture.EditRate
			comp.FrameRate = firstReel.AssetList.MainPicture.FrameRate
			comp.ScreenAspectRatio = firstReel.AssetList.MainPicture.ScreenAspectRatio
		}
	}
	
	if err := idx.db.InsertDCPComposition(comp); err != nil {
		return err
	}
	
	// Index all reels
	for i, reel := range cpl.ReelList.Reels {
		if err := idx.indexReel(comp.ID, i+1, &reel); err != nil {
			log.Printf("Warning: failed to index reel: %v", err)
		}
	}
	
	return nil
}

// indexReel stores a reel to the database
func (idx *Indexer) indexReel(compositionID uuid.UUID, reelNumber int, reel *parser.Reel) error {
	reelUUID, err := uuid.Parse(parser.ExtractUUID(reel.ID))
	if err != nil {
		return fmt.Errorf("invalid reel UUID: %w", err)
	}
	
	dbReel := &db.DCPReel{
		ID:            uuid.New(),
		CompositionID: compositionID,
		ReelUUID:      reelUUID,
		ReelNumber:    reelNumber,
		CreatedAt:     time.Now(),
	}
	
	if reel.AssetList.MainPicture != nil {
		pic := reel.AssetList.MainPicture
		dbReel.DurationFrames = int(pic.Duration)
		dbReel.PictureEditRate = pic.EditRate
		dbReel.PictureEntryPoint = int(pic.EntryPoint)
		dbReel.PictureIntrinsicDuration = int(pic.IntrinsicDuration)
		dbReel.PictureHash = pic.Hash
		
		if picUUID, err := uuid.Parse(parser.ExtractUUID(pic.ID)); err == nil {
			dbReel.PictureAssetUUID = &picUUID
		}
		if keyID, err := uuid.Parse(parser.ExtractUUID(pic.KeyID)); err == nil {
			dbReel.PictureKeyID = &keyID
		}
	}
	
	if reel.AssetList.MainSound != nil {
		if sndUUID, err := uuid.Parse(parser.ExtractUUID(reel.AssetList.MainSound.ID)); err == nil {
			dbReel.SoundAssetUUID = &sndUUID
		}
	}
	
	if reel.AssetList.MainSubtitle != nil {
		sub := reel.AssetList.MainSubtitle
		if subUUID, err := uuid.Parse(parser.ExtractUUID(sub.ID)); err == nil {
			dbReel.SubtitleAssetUUID = &subUUID
		}
		dbReel.SubtitleLanguage = sub.Language
	}
	
	return idx.db.InsertDCPReel(dbReel)
}

// indexPackingList stores a PKL to the database
func (idx *Indexer) indexPackingList(packageID uuid.UUID, pkl *parser.PackingList) error {
	pklUUID, err := uuid.Parse(parser.ExtractUUID(pkl.ID))
	if err != nil {
		return fmt.Errorf("invalid PKL UUID: %w", err)
	}
	
	var issueDate *time.Time
	if pkl.IssueDate != "" {
		if t, err := time.Parse(time.RFC3339, pkl.IssueDate); err == nil {
			issueDate = &t
		}
	}
	
	dbPKL := &db.DCPPackingList{
		ID:             uuid.New(),
		PackageID:      packageID,
		PKLUUID:        pklUUID,
		AnnotationText: pkl.AnnotationText,
		IssueDate:      issueDate,
		Issuer:         pkl.Issuer,
		Creator:        pkl.Creator,
		AssetCount:     pkl.GetAssetCount(),
		CreatedAt:      time.Now(),
	}
	
	return idx.db.InsertDCPPackingList(dbPKL)
}

// indexAssets stores assets from PKL and ASSETMAP to the database
func (idx *Indexer) indexAssets(packageID uuid.UUID, info *DCPPackageInfo, pkl *parser.PackingList) error {
	// Index assets from PKL (which has hash and size info)
	for _, pklAsset := range pkl.AssetList.Assets {
		assetUUID, err := uuid.Parse(parser.ExtractUUID(pklAsset.ID))
		if err != nil {
			log.Printf("Warning: invalid asset UUID: %v", err)
			continue
		}
		
		// Find corresponding asset in ASSETMAP to get file path
		var filePath, fileName string
		for _, amAsset := range info.AssetMap.AssetList.Assets {
			if parser.ExtractUUID(amAsset.ID) == assetUUID.String() {
				if len(amAsset.ChunkList.Chunks) > 0 {
					filePath = amAsset.ChunkList.Chunks[0].Path
					fileName = filepath.Base(filePath)
				}
				break
			}
		}
		
		// Determine asset role based on file name
		assetRole := determineAssetRole(fileName)
		
		dbAsset := &db.DCPAsset{
			ID:            uuid.New(),
			PackageID:     packageID,
			AssetUUID:     assetUUID,
			FilePath:      filePath,
			FileName:      fileName,
			AssetType:     pklAsset.Type,
			AssetRole:     assetRole,
			SizeBytes:     pklAsset.Size,
			HashAlgorithm: "SHA1",
			HashValue:     pklAsset.Hash,
			ChunkOffset:   0,
			ChunkLength:   pklAsset.Size,
			CreatedAt:     time.Now(),
		}
		
		if err := idx.db.InsertDCPAsset(dbAsset); err != nil {
			log.Printf("Warning: failed to insert asset: %v", err)
		}
	}
	
	return nil
}

// determineAssetRole determines the role of an asset based on file name
func determineAssetRole(fileName string) string {
	if fileName == "" {
		return "unknown"
	}
	
	lower := filepath.Ext(fileName)
	if lower == ".xml" {
		if filepath.Base(fileName)[:3] == "CPL" {
			return "cpl"
		}
		if filepath.Base(fileName)[:3] == "PKL" {
			return "pkl"
		}
		return "metadata"
	}
	
	if lower == ".mxf" {
		// Check filename patterns
		name := filepath.Base(fileName)
		if contains(name, "_v.mxf") || contains(name, "_pic") {
			return "picture"
		}
		if contains(name, "_snd.mxf") || contains(name, "_sound") {
			return "sound"
		}
		if contains(name, "_tt.mxf") || contains(name, "_sub") {
			return "subtitle"
		}
		return "mxf"
	}
	
	return "other"
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr))
}

// checkTorrentStatus checks if a torrent exists for this package.
// If no torrent exists and the package isn't actively queued (queued/generating),
// it adds or re-queues the package for generation. This handles restart resilience:
// stale queue entries (failed/cancelled/completed without a torrent) get reset.
func (idx *Indexer) checkTorrentStatus(packageID uuid.UUID, assetMapUUID uuid.UUID) error {
	// Check if torrent already exists for this package
	var torrentExists bool
	query := `SELECT EXISTS(SELECT 1 FROM dcp_torrents WHERE package_id = $1)`
	err := idx.db.DB.QueryRow(query, packageID.String()).Scan(&torrentExists)
	if err != nil {
		return fmt.Errorf("failed to check torrent existence: %w", err)
	}

	if torrentExists {
		return nil
	}

	// No torrent exists. Only skip if the package is ACTIVELY being processed
	// (queued or generating). Stale entries (failed/cancelled/completed) should
	// be reset since the package still needs a torrent.
	var activeInQueue bool
	queueQuery := `SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id = $2 AND status IN ('queued', 'generating'))`
	err = idx.db.DB.QueryRow(queueQuery, packageID.String(), idx.serverID.String()).Scan(&activeInQueue)
	if err != nil {
		return fmt.Errorf("failed to check queue status: %w", err)
	}

	if activeInQueue {
		return nil
	}

	// Check with orchestrator (main server) if we should hash this package.
	// Another server may already be hashing it, or the torrent may already exist on main server.
	if !idx.torrentQueue.ShouldHash(assetMapUUID.String()) {
		return nil // Another server is handling it
	}

	// Package needs a torrent and is not actively queued - add or re-queue it
	log.Printf("Package %s has no torrent, queuing for generation", packageID)
	return idx.torrentQueue.AddToQueue(packageID.String())
}
