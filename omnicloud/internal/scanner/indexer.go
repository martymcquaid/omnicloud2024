package scanner

import (
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/parser"
	torrentpkg "github.com/omnicloud/omnicloud/internal/torrent"
)

// TorrentQueue interface for adding packages to torrent queue
type TorrentQueue interface {
	AddToQueue(packageID string) error
}

// Indexer handles storing DCP metadata to the database
type Indexer struct {
	db               *db.DB
	serverID         uuid.UUID
	torrentQueue     TorrentQueue
	torrentDownloader *torrentpkg.TorrentDownloader
	isClientMode     bool
	mainServerURL    string
	macAddress       string
}

// NewIndexer creates a new indexer instance
func NewIndexer(database *db.DB, serverID uuid.UUID) *Indexer {
	return &Indexer{
		db:           database,
		serverID:     serverID,
		torrentQueue: nil, // Set later with SetTorrentQueue
		isClientMode: false,
	}
}

// SetClientMode configures the indexer for client mode with torrent downloading
func (idx *Indexer) SetClientMode(mainServerURL, macAddress string) {
	idx.isClientMode = true
	idx.mainServerURL = mainServerURL
	idx.macAddress = macAddress
	idx.torrentDownloader = torrentpkg.NewTorrentDownloader(idx.db.DB, mainServerURL, idx.serverID.String(), macAddress)
}

// SetTorrentQueue sets the torrent queue manager
func (idx *Indexer) SetTorrentQueue(queue TorrentQueue) {
	idx.torrentQueue = queue
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
		comp.Luminance = metadata.Luminance
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
		dbReel.DurationFrames = pic.Duration
		dbReel.PictureEditRate = pic.EditRate
		dbReel.PictureEntryPoint = pic.EntryPoint
		dbReel.PictureIntrinsicDuration = pic.IntrinsicDuration
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

// checkTorrentStatus checks if a torrent exists for this package
// If not, adds it to the generation queue
func (idx *Indexer) checkTorrentStatus(packageID uuid.UUID, assetMapUUID uuid.UUID) error {
	// Check if torrent already exists for this package
	var torrentExists bool
	query := `SELECT EXISTS(SELECT 1 FROM dcp_torrents WHERE package_id = $1)`
	err := idx.db.DB.QueryRow(query, packageID.String()).Scan(&torrentExists)
	if err != nil {
		return fmt.Errorf("failed to check torrent existence: %w", err)
	}

	if torrentExists {
		log.Printf("Torrent already exists for package %s", packageID)
		return nil
	}

	// For client servers, try to download existing torrent from main server first
	if idx.isClientMode && idx.torrentDownloader != nil {
		// Get package path from inventory
		var packagePath string
		pathQuery := `SELECT local_path FROM server_dcp_inventory WHERE server_id = $1 AND package_id = $2`
		err := idx.db.DB.QueryRow(pathQuery, idx.serverID.String(), packageID.String()).Scan(&packagePath)
		if err == nil && packagePath != "" {
			downloaded, err := idx.torrentDownloader.TryDownloadExistingTorrent(packageID, packagePath)
			if err != nil {
				log.Printf("Warning: failed to download torrent from main server: %v", err)
				// Fall through to local generation
			} else if downloaded {
				log.Printf("Successfully downloaded existing torrent for package %s, skipping local generation", packageID)
				return nil
			}
			// If torrent doesn't exist on main server, fall through to local generation
		}
	}

	// Check if already in queue
	var inQueue bool
	queueQuery := `SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id = $2)`
	err = idx.db.DB.QueryRow(queueQuery, packageID.String(), idx.serverID.String()).Scan(&inQueue)
	if err != nil {
		return fmt.Errorf("failed to check queue status: %w", err)
	}

	if inQueue {
		log.Printf("Package %s already in torrent queue", packageID)
		return nil
	}

	// Add to torrent generation queue
	log.Printf("Adding package %s to torrent generation queue", packageID)
	return idx.torrentQueue.AddToQueue(packageID.String())
}
