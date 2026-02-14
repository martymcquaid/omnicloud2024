package db

import (
	"database/sql"

	"github.com/google/uuid"
)

// UpsertServer inserts or updates a server record
func (db *DB) UpsertServer(server *Server) error {
	query := `
		INSERT INTO servers (id, name, location, api_url, mac_address, registration_key_hash, is_authorized, last_seen, storage_capacity_tb, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			location = EXCLUDED.location,
			api_url = EXCLUDED.api_url,
			mac_address = EXCLUDED.mac_address,
			registration_key_hash = EXCLUDED.registration_key_hash,
			is_authorized = EXCLUDED.is_authorized,
			last_seen = EXCLUDED.last_seen,
			storage_capacity_tb = EXCLUDED.storage_capacity_tb,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`
	
	return db.QueryRow(query,
		server.ID, server.Name, server.Location, server.APIURL,
		server.MACAddress, server.RegistrationKeyHash, server.IsAuthorized,
		server.LastSeen, server.StorageCapacityTB,
		server.CreatedAt, server.UpdatedAt,
	).Scan(&server.ID)
}

// GetServer retrieves a server by ID
func (db *DB) GetServer(id uuid.UUID) (*Server, error) {
	query := `SELECT id, name, location, api_url, COALESCE(mac_address, ''), COALESCE(registration_key_hash, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, created_at, updated_at
			  FROM servers WHERE id = $1`
	server := &Server{}
	err := db.QueryRow(query, id).Scan(
		&server.ID, &server.Name, &server.Location, &server.APIURL,
		&server.MACAddress, &server.RegistrationKeyHash, &server.IsAuthorized,
		&server.LastSeen, &server.StorageCapacityTB,
		&server.CreatedAt, &server.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return server, err
}

// GetServerByName retrieves a server by name
func (db *DB) GetServerByName(name string) (*Server, error) {
	query := `SELECT id, name, location, api_url, COALESCE(mac_address, ''), COALESCE(registration_key_hash, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, created_at, updated_at
			  FROM servers WHERE name = $1`
	
	server := &Server{}
	err := db.QueryRow(query, name).Scan(
		&server.ID, &server.Name, &server.Location, &server.APIURL,
		&server.MACAddress, &server.RegistrationKeyHash, &server.IsAuthorized,
		&server.LastSeen, &server.StorageCapacityTB,
		&server.CreatedAt, &server.UpdatedAt,
	)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return server, err
}

// GetServerByMACAddress retrieves a server by MAC address
func (db *DB) GetServerByMACAddress(macAddress string) (*Server, error) {
	query := `SELECT id, name, location, api_url, COALESCE(mac_address, ''), COALESCE(registration_key_hash, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, created_at, updated_at
			  FROM servers WHERE mac_address = $1`
	
	server := &Server{}
	err := db.QueryRow(query, macAddress).Scan(
		&server.ID, &server.Name, &server.Location, &server.APIURL,
		&server.MACAddress, &server.RegistrationKeyHash, &server.IsAuthorized,
		&server.LastSeen, &server.StorageCapacityTB,
		&server.CreatedAt, &server.UpdatedAt,
	)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return server, err
}

// UpsertDCPPackage inserts or updates a DCP package
func (db *DB) UpsertDCPPackage(pkg *DCPPackage) error {
	query := `
		INSERT INTO dcp_packages (
			id, assetmap_uuid, package_name, content_title, content_kind,
			issue_date, issuer, creator, annotation_text, volume_count,
			total_size_bytes, file_count, discovered_at, last_verified,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (assetmap_uuid) DO UPDATE SET
			package_name = EXCLUDED.package_name,
			content_title = EXCLUDED.content_title,
			content_kind = EXCLUDED.content_kind,
			issue_date = EXCLUDED.issue_date,
			issuer = EXCLUDED.issuer,
			creator = EXCLUDED.creator,
			annotation_text = EXCLUDED.annotation_text,
			volume_count = EXCLUDED.volume_count,
			total_size_bytes = EXCLUDED.total_size_bytes,
			file_count = EXCLUDED.file_count,
			last_verified = EXCLUDED.last_verified,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`
	
	return db.QueryRow(query,
		pkg.ID, pkg.AssetMapUUID, pkg.PackageName, pkg.ContentTitle, pkg.ContentKind,
		pkg.IssueDate, pkg.Issuer, pkg.Creator, pkg.AnnotationText, pkg.VolumeCount,
		pkg.TotalSizeBytes, pkg.FileCount, pkg.DiscoveredAt, pkg.LastVerified,
		pkg.CreatedAt, pkg.UpdatedAt,
	).Scan(&pkg.ID)
}

// GetDCPPackageByAssetMapUUID retrieves a package by its ASSETMAP UUID
func (db *DB) GetDCPPackageByAssetMapUUID(assetMapUUID uuid.UUID) (*DCPPackage, error) {
	query := `SELECT id, assetmap_uuid, package_name, content_title, content_kind,
			  issue_date, issuer, creator, annotation_text, volume_count,
			  total_size_bytes, file_count, discovered_at, last_verified,
			  created_at, updated_at
			  FROM dcp_packages WHERE assetmap_uuid = $1`
	
	pkg := &DCPPackage{}
	err := db.QueryRow(query, assetMapUUID).Scan(
		&pkg.ID, &pkg.AssetMapUUID, &pkg.PackageName, &pkg.ContentTitle, &pkg.ContentKind,
		&pkg.IssueDate, &pkg.Issuer, &pkg.Creator, &pkg.AnnotationText, &pkg.VolumeCount,
		&pkg.TotalSizeBytes, &pkg.FileCount, &pkg.DiscoveredAt, &pkg.LastVerified,
		&pkg.CreatedAt, &pkg.UpdatedAt,
	)
	
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return pkg, err
}

// InsertDCPComposition inserts a composition record
func (db *DB) InsertDCPComposition(comp *DCPComposition) error {
	query := `
		INSERT INTO dcp_compositions (
			id, package_id, cpl_uuid, content_title_text, full_content_title,
			content_kind, content_version_id, label_text, issue_date, issuer,
			creator, edit_rate, frame_rate, screen_aspect_ratio,
			resolution_width, resolution_height, main_sound_configuration,
			main_sound_sample_rate, luminance, release_territory, distributor,
			facility, reel_count, total_duration_frames, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
		ON CONFLICT DO NOTHING`
	
	_, err := db.Exec(query,
		comp.ID, comp.PackageID, comp.CPLUUID, comp.ContentTitleText, comp.FullContentTitle,
		comp.ContentKind, comp.ContentVersionID, comp.LabelText, comp.IssueDate, comp.Issuer,
		comp.Creator, comp.EditRate, comp.FrameRate, comp.ScreenAspectRatio,
		comp.ResolutionWidth, comp.ResolutionHeight, comp.MainSoundConfiguration,
		comp.MainSoundSampleRate, comp.Luminance, comp.ReleaseTerritory, comp.Distributor,
		comp.Facility, comp.ReelCount, comp.TotalDurationFrames, comp.CreatedAt, comp.UpdatedAt,
	)
	return err
}

// InsertDCPReel inserts a reel record
func (db *DB) InsertDCPReel(reel *DCPReel) error {
	query := `
		INSERT INTO dcp_reels (
			id, composition_id, reel_uuid, reel_number, duration_frames,
			picture_asset_uuid, picture_edit_rate, picture_entry_point,
			picture_intrinsic_duration, picture_key_id, picture_hash,
			sound_asset_uuid, sound_configuration, subtitle_asset_uuid,
			subtitle_language, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT DO NOTHING`
	
	_, err := db.Exec(query,
		reel.ID, reel.CompositionID, reel.ReelUUID, reel.ReelNumber, reel.DurationFrames,
		reel.PictureAssetUUID, reel.PictureEditRate, reel.PictureEntryPoint,
		reel.PictureIntrinsicDuration, reel.PictureKeyID, reel.PictureHash,
		reel.SoundAssetUUID, reel.SoundConfiguration, reel.SubtitleAssetUUID,
		reel.SubtitleLanguage, reel.CreatedAt,
	)
	return err
}

// InsertDCPAsset inserts an asset record
func (db *DB) InsertDCPAsset(asset *DCPAsset) error {
	query := `
		INSERT INTO dcp_assets (
			id, package_id, asset_uuid, file_path, file_name, asset_type,
			asset_role, size_bytes, hash_algorithm, hash_value,
			chunk_offset, chunk_length, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING`
	
	_, err := db.Exec(query,
		asset.ID, asset.PackageID, asset.AssetUUID, asset.FilePath, asset.FileName,
		asset.AssetType, asset.AssetRole, asset.SizeBytes, asset.HashAlgorithm,
		asset.HashValue, asset.ChunkOffset, asset.ChunkLength, asset.CreatedAt,
	)
	return err
}

// InsertDCPPackingList inserts a packing list record
func (db *DB) InsertDCPPackingList(pkl *DCPPackingList) error {
	query := `
		INSERT INTO dcp_packing_lists (
			id, package_id, pkl_uuid, annotation_text, issue_date,
			issuer, creator, asset_count, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT DO NOTHING`
	
	_, err := db.Exec(query,
		pkl.ID, pkl.PackageID, pkl.PKLUUID, pkl.AnnotationText, pkl.IssueDate,
		pkl.Issuer, pkl.Creator, pkl.AssetCount, pkl.CreatedAt,
	)
	return err
}

// UpsertServerDCPInventory inserts or updates inventory record
func (db *DB) UpsertServerDCPInventory(inv *ServerDCPInventory) error {
	query := `
		INSERT INTO server_dcp_inventory (
			id, server_id, package_id, local_path, status, last_verified, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (server_id, package_id) DO UPDATE SET
			local_path = EXCLUDED.local_path,
			status = EXCLUDED.status,
			last_verified = EXCLUDED.last_verified,
			updated_at = CURRENT_TIMESTAMP`
	
	_, err := db.Exec(query,
		inv.ID, inv.ServerID, inv.PackageID, inv.LocalPath,
		inv.Status, inv.LastVerified, inv.CreatedAt, inv.UpdatedAt,
	)
	return err
}

// GetServerInventory retrieves all inventory entries for a specific server
func (db *DB) GetServerInventory(serverID uuid.UUID) ([]*ServerDCPInventory, error) {
	query := `
		SELECT id, server_id, package_id, local_path, status, last_verified, created_at, updated_at
		FROM server_dcp_inventory
		WHERE server_id = $1
		ORDER BY created_at DESC`
	
	rows, err := db.Query(query, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var inventory []*ServerDCPInventory
	for rows.Next() {
		inv := &ServerDCPInventory{}
		err := rows.Scan(
			&inv.ID, &inv.ServerID, &inv.PackageID, &inv.LocalPath,
			&inv.Status, &inv.LastVerified, &inv.CreatedAt, &inv.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		inventory = append(inventory, inv)
	}
	
	if err = rows.Err(); err != nil {
		return nil, err
	}
	
	return inventory, nil
}

// DeleteServerDCPInventory removes an inventory entry
func (db *DB) DeleteServerDCPInventory(inventoryID uuid.UUID) error {
	query := `DELETE FROM server_dcp_inventory WHERE id = $1`
	_, err := db.Exec(query, inventoryID)
	return err
}

// CreateScanLog creates a new scan log entry
func (db *DB) CreateScanLog(log *ScanLog) error {
	query := `
		INSERT INTO scan_logs (
			id, server_id, scan_type, started_at, completed_at,
			packages_found, packages_added, packages_updated, packages_removed,
			errors, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id`
	
	return db.QueryRow(query,
		log.ID, log.ServerID, log.ScanType, log.StartedAt, log.CompletedAt,
		log.PackagesFound, log.PackagesAdded, log.PackagesUpdated, log.PackagesRemoved,
		log.Errors, log.Status,
	).Scan(&log.ID)
}

// UpdateScanLog updates an existing scan log
func (db *DB) UpdateScanLog(log *ScanLog) error {
	query := `
		UPDATE scan_logs SET
			completed_at = $1,
			packages_found = $2,
			packages_added = $3,
			packages_updated = $4,
			packages_removed = $5,
			errors = $6,
			status = $7
		WHERE id = $8`
	
	_, err := db.Exec(query,
		log.CompletedAt, log.PackagesFound, log.PackagesAdded,
		log.PackagesUpdated, log.PackagesRemoved, log.Errors, log.Status, log.ID,
	)
	return err
}

// GetLatestScanLog returns the most recent scan log for a server
func (db *DB) GetLatestScanLog(serverID uuid.UUID) (*ScanLog, error) {
	query := `
		SELECT id, server_id, scan_type, started_at, completed_at,
		       packages_found, packages_added, packages_updated, packages_removed,
		       errors, status
		FROM scan_logs
		WHERE server_id = $1
		ORDER BY started_at DESC
		LIMIT 1`
	log := &ScanLog{}
	err := db.QueryRow(query, serverID).Scan(
		&log.ID, &log.ServerID, &log.ScanType, &log.StartedAt, &log.CompletedAt,
		&log.PackagesFound, &log.PackagesAdded, &log.PackagesUpdated, &log.PackagesRemoved,
		&log.Errors, &log.Status,
	)
	if err != nil {
		return nil, err
	}
	return log, nil
}

// UpdateScanLogProgress updates progress fields of a scan log during an in-progress scan
func (db *DB) UpdateScanLogProgress(scanLogID uuid.UUID, packagesAdded, packagesUpdated int) error {
	query := `UPDATE scan_logs SET packages_added = $1, packages_updated = $2 WHERE id = $3`
	_, err := db.Exec(query, packagesAdded, packagesUpdated, scanLogID)
	return err
}

// CountDCPPackages returns the total number of packages
func (db *DB) CountDCPPackages() (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM dcp_packages").Scan(&count)
	return count, err
}
