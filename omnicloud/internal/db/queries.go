package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// HashPassword creates a salted SHA-256 hash: "salt:hash"
func HashPassword(password string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	saltHex := hex.EncodeToString(salt)
	hash := sha256.Sum256([]byte(saltHex + ":" + password))
	return saltHex + ":" + hex.EncodeToString(hash[:])
}

// verifyPassword checks a password against a "salt:hash" string
func verifyPassword(password, storedHash string) bool {
	parts := strings.SplitN(storedHash, ":", 2)
	if len(parts) != 2 {
		return false
	}
	saltHex := parts[0]
	hash := sha256.Sum256([]byte(saltHex + ":" + password))
	return hex.EncodeToString(hash[:]) == parts[1]
}

// SeedDefaultUser inserts the default admin user if no users exist
func (db *DB) SeedDefaultUser(username, password string) error {
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return fmt.Errorf("checking users count: %v", err)
	}
	if count > 0 {
		return nil // Users already exist
	}
	query := `INSERT INTO users (id, username, password_hash, role, is_active)
	          VALUES (uuid_generate_v4(), $1, $2, 'admin', true)
	          ON CONFLICT (username) DO NOTHING`
	_, err := db.Exec(query, username, HashPassword(password))
	return err
}

// UpsertServer inserts or updates a server record. On conflict, display_name is NOT updated
// so client re-registration/sync never overwrites the user-defined display name.
func (db *DB) UpsertServer(server *Server) error {
	query := `
		INSERT INTO servers (id, name, location, api_url, mac_address, registration_key_hash, is_authorized, last_seen, storage_capacity_tb, created_at, updated_at, display_name)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF(TRIM($12), ''))
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
		server.DisplayName,
	).Scan(&server.ID)
}

// GetServer retrieves a server by ID
func (db *DB) GetServer(id uuid.UUID) (*Server, error) {
	query := `SELECT id, name, COALESCE(display_name, ''), location, api_url, COALESCE(mac_address, ''), COALESCE(registration_key_hash, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, created_at, updated_at
			  FROM servers WHERE id = $1`
	server := &Server{}
	err := db.QueryRow(query, id).Scan(
		&server.ID, &server.Name, &server.DisplayName, &server.Location, &server.APIURL,
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
	query := `SELECT id, name, COALESCE(display_name, ''), location, api_url, COALESCE(mac_address, ''), COALESCE(registration_key_hash, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, created_at, updated_at
			  FROM servers WHERE name = $1`
	server := &Server{}
	err := db.QueryRow(query, name).Scan(
		&server.ID, &server.Name, &server.DisplayName, &server.Location, &server.APIURL,
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
	query := `SELECT id, name, COALESCE(display_name, ''), location, api_url, COALESCE(mac_address, ''), COALESCE(registration_key_hash, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, created_at, updated_at
			  FROM servers WHERE mac_address = $1`
	server := &Server{}
	err := db.QueryRow(query, macAddress).Scan(
		&server.ID, &server.Name, &server.DisplayName, &server.Location, &server.APIURL,
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

// GetDCPPackageByCPLUUID finds a package that contains a composition with the given CPL UUID.
// Returns nil if not found. Used for cross-site deduplication when ASSETMAP UUIDs differ
// but the actual film content (CPL) is identical (e.g. RosettaBridge site deliveries).
func (db *DB) GetDCPPackageByCPLUUID(cplUUID uuid.UUID) (*DCPPackage, error) {
	query := `
		SELECT p.id, p.assetmap_uuid, p.package_name, p.content_title, p.content_kind,
		       p.issue_date, p.issuer, p.creator, p.annotation_text, p.volume_count,
		       p.total_size_bytes, p.file_count, p.discovered_at, p.last_verified,
		       p.created_at, p.updated_at
		FROM dcp_packages p
		JOIN dcp_compositions c ON c.package_id = p.id
		WHERE c.cpl_uuid = $1
		LIMIT 1`

	pkg := &DCPPackage{}
	err := db.QueryRow(query, cplUUID).Scan(
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

// TorrentRecord holds minimal torrent info needed for cross-seeding deduplication.
type TorrentRecord struct {
	ID          string
	InfoHash    string
	TorrentFile []byte
}

// GetTorrentByPackageID returns the torrent file and info_hash for a package, or nil if none exists.
func (db *DB) GetTorrentByPackageID(packageID uuid.UUID) (*TorrentRecord, error) {
	query := `SELECT id, info_hash, torrent_file FROM dcp_torrents WHERE package_id = $1 LIMIT 1`
	t := &TorrentRecord{}
	err := db.QueryRow(query, packageID).Scan(&t.ID, &t.InfoHash, &t.TorrentFile)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
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
		ON CONFLICT (package_id, cpl_uuid) DO UPDATE SET
			content_title_text = EXCLUDED.content_title_text,
			full_content_title = EXCLUDED.full_content_title,
			content_kind = EXCLUDED.content_kind,
			edit_rate = EXCLUDED.edit_rate,
			frame_rate = EXCLUDED.frame_rate,
			screen_aspect_ratio = EXCLUDED.screen_aspect_ratio,
			resolution_width = EXCLUDED.resolution_width,
			resolution_height = EXCLUDED.resolution_height,
			main_sound_configuration = EXCLUDED.main_sound_configuration,
			main_sound_sample_rate = EXCLUDED.main_sound_sample_rate,
			luminance = EXCLUDED.luminance,
			reel_count = EXCLUDED.reel_count,
			total_duration_frames = EXCLUDED.total_duration_frames,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id`

	return db.QueryRow(query,
		comp.ID, comp.PackageID, comp.CPLUUID, comp.ContentTitleText, comp.FullContentTitle,
		comp.ContentKind, comp.ContentVersionID, comp.LabelText, comp.IssueDate, comp.Issuer,
		comp.Creator, comp.EditRate, comp.FrameRate, comp.ScreenAspectRatio,
		comp.ResolutionWidth, comp.ResolutionHeight, comp.MainSoundConfiguration,
		comp.MainSoundSampleRate, comp.Luminance, comp.ReleaseTerritory, comp.Distributor,
		comp.Facility, comp.ReelCount, comp.TotalDurationFrames, comp.CreatedAt, comp.UpdatedAt,
	).Scan(&comp.ID)
}

// InsertDCPReel inserts or updates a reel record
func (db *DB) InsertDCPReel(reel *DCPReel) error {
	query := `
		INSERT INTO dcp_reels (
			id, composition_id, reel_uuid, reel_number, duration_frames,
			picture_asset_uuid, picture_edit_rate, picture_entry_point,
			picture_intrinsic_duration, picture_key_id, picture_hash,
			sound_asset_uuid, sound_configuration, subtitle_asset_uuid,
			subtitle_language, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (composition_id, reel_uuid) DO NOTHING`

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

// AuthenticateUser verifies username/password using salted SHA-256.
// Returns the user if credentials are valid, nil otherwise.
func (db *DB) AuthenticateUser(username, password string) (*User, error) {
	query := `SELECT id, username, password_hash, role, is_active, created_at, updated_at
	          FROM users
	          WHERE username = $1 AND is_active = true`
	u := &User{}
	err := db.QueryRow(query, username).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !verifyPassword(password, u.PasswordHash) {
		return nil, nil
	}
	return u, nil
}

// CreateSession inserts a new session token
func (db *DB) CreateSession(session *UserSession) error {
	query := `INSERT INTO user_sessions (token, user_id, created_at, expires_at)
	          VALUES ($1, $2, $3, $4)`
	_, err := db.Exec(query, session.Token, session.UserID, session.CreatedAt, session.ExpiresAt)
	return err
}

// GetSession retrieves a valid (non-expired) session by token
func (db *DB) GetSession(token string) (*UserSession, error) {
	query := `SELECT token, user_id, created_at, expires_at
	          FROM user_sessions
	          WHERE token = $1 AND expires_at > NOW()`
	s := &UserSession{}
	err := db.QueryRow(query, token).Scan(&s.Token, &s.UserID, &s.CreatedAt, &s.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// DeleteSession removes a session (logout)
func (db *DB) DeleteSession(token string) error {
	_, err := db.Exec(`DELETE FROM user_sessions WHERE token = $1`, token)
	return err
}

// DeleteExpiredSessions removes all expired sessions
func (db *DB) DeleteExpiredSessions() error {
	_, err := db.Exec(`DELETE FROM user_sessions WHERE expires_at < NOW()`)
	return err
}

// GetUserByID retrieves a user by ID
func (db *DB) GetUserByID(id uuid.UUID) (*User, error) {
	query := `SELECT id, username, role, is_active, created_at, updated_at
	          FROM users WHERE id = $1`
	u := &User{}
	err := db.QueryRow(query, id).Scan(
		&u.ID, &u.Username, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// ListUsers returns all users (without password hashes)
func (db *DB) ListUsers() ([]*User, error) {
	query := `SELECT id, username, role, is_active, created_at, updated_at
	          FROM users ORDER BY created_at ASC`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CreateUser inserts a new user with a hashed password
func (db *DB) CreateUser(username, password, role string) (*User, error) {
	u := &User{}
	query := `INSERT INTO users (id, username, password_hash, role, is_active)
	          VALUES (uuid_generate_v4(), $1, $2, $3, true)
	          RETURNING id, username, role, is_active, created_at, updated_at`
	err := db.QueryRow(query, username, HashPassword(password), role).Scan(
		&u.ID, &u.Username, &u.Role, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	return u, err
}

// UpdateUser modifies a user's username, role, and active status
func (db *DB) UpdateUser(id uuid.UUID, username, role string, isActive bool) error {
	query := `UPDATE users SET username = $1, role = $2, is_active = $3 WHERE id = $4`
	_, err := db.Exec(query, username, role, isActive, id)
	return err
}

// UpdateUserPassword changes a user's password
func (db *DB) UpdateUserPassword(id uuid.UUID, newPassword string) error {
	query := `UPDATE users SET password_hash = $1 WHERE id = $2`
	_, err := db.Exec(query, HashPassword(newPassword), id)
	return err
}

// DeleteUser removes a user and invalidates all their sessions
func (db *DB) DeleteUser(id uuid.UUID) error {
	_, err := db.Exec(`DELETE FROM user_sessions WHERE user_id = $1`, id)
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM users WHERE id = $1`, id)
	return err
}

// DeleteUserSessions invalidates all sessions for a user
func (db *DB) DeleteUserSessions(userID uuid.UUID) error {
	_, err := db.Exec(`DELETE FROM user_sessions WHERE user_id = $1`, userID)
	return err
}

// CountActiveAdmins returns how many active admin users exist (excluding a specific ID)
func (db *DB) CountActiveAdmins(excludeID uuid.UUID) (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND is_active = true AND id != $1`, excludeID).Scan(&count)
	return count, err
}

// ListRolePermissions returns all role permission entries
func (db *DB) ListRolePermissions() ([]*RolePermission, error) {
	query := `SELECT role, allowed_pages, COALESCE(description, ''), created_at, updated_at
	          FROM role_permissions ORDER BY role ASC`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []*RolePermission
	for rows.Next() {
		p := &RolePermission{}
		if err := rows.Scan(&p.Role, &p.AllowedPages, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// GetRolePermissions returns the permission entry for a specific role
func (db *DB) GetRolePermissions(role string) (*RolePermission, error) {
	query := `SELECT role, allowed_pages, COALESCE(description, ''), created_at, updated_at
	          FROM role_permissions WHERE role = $1`
	p := &RolePermission{}
	err := db.QueryRow(query, role).Scan(&p.Role, &p.AllowedPages, &p.Description, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// UpdateRolePermissions updates the allowed pages and description for a role
func (db *DB) UpdateRolePermissions(role, allowedPages, description string) error {
	query := `INSERT INTO role_permissions (role, allowed_pages, description)
	          VALUES ($1, $2, $3)
	          ON CONFLICT (role) DO UPDATE SET allowed_pages = $2, description = $3`
	_, err := db.Exec(query, role, allowedPages, description)
	return err
}

// --- Activity Log ---

// CreateActivityLog inserts an activity log entry
func (db *DB) CreateActivityLog(entry *ActivityLog) error {
	query := `INSERT INTO activity_logs (user_id, username, action, category, resource_type, resource_id, resource_name, details, ip_address, status)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := db.Exec(query, entry.UserID, entry.Username, entry.Action, entry.Category,
		entry.ResourceType, entry.ResourceID, entry.ResourceName, entry.Details, entry.IPAddress, entry.Status)
	return err
}

// ListActivityLogs returns paginated activity logs with optional filters. Returns (logs, totalCount, error).
func (db *DB) ListActivityLogs(filter ActivityLogFilter) ([]ActivityLog, int, error) {
	// Build WHERE clause dynamically
	where := "WHERE 1=1"
	args := []interface{}{}
	argN := 1

	if filter.Category != "" {
		where += fmt.Sprintf(" AND category = $%d", argN)
		args = append(args, filter.Category)
		argN++
	}
	if filter.Action != "" {
		where += fmt.Sprintf(" AND action = $%d", argN)
		args = append(args, filter.Action)
		argN++
	}
	if filter.UserID != nil {
		where += fmt.Sprintf(" AND user_id = $%d", argN)
		args = append(args, *filter.UserID)
		argN++
	}
	if filter.Search != "" {
		where += fmt.Sprintf(" AND (username ILIKE $%d OR action ILIKE $%d OR resource_name ILIKE $%d OR details ILIKE $%d)", argN, argN, argN, argN)
		args = append(args, "%"+filter.Search+"%")
		argN++
	}
	if filter.StartDate != nil {
		where += fmt.Sprintf(" AND created_at >= $%d", argN)
		args = append(args, *filter.StartDate)
		argN++
	}
	if filter.EndDate != nil {
		where += fmt.Sprintf(" AND created_at <= $%d", argN)
		args = append(args, *filter.EndDate)
		argN++
	}

	// Count total
	var total int
	countQuery := "SELECT COUNT(*) FROM activity_logs " + where
	if err := db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Fetch page
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	dataQuery := fmt.Sprintf(`SELECT id, user_id, username, action, category, resource_type, resource_id, resource_name, details, ip_address, status, created_at
		FROM activity_logs %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, argN, argN+1)
	args = append(args, limit, offset)

	rows, err := db.Query(dataQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []ActivityLog
	for rows.Next() {
		var l ActivityLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.Username, &l.Action, &l.Category,
			&l.ResourceType, &l.ResourceID, &l.ResourceName, &l.Details, &l.IPAddress, &l.Status, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []ActivityLog{}
	}
	return logs, total, nil
}

// GetActivityLogStats returns summary statistics for the activity log dashboard
func (db *DB) GetActivityLogStats() (int, int, map[string]int, error) {
	var total, todayCount int
	db.QueryRow("SELECT COUNT(*) FROM activity_logs").Scan(&total)
	db.QueryRow("SELECT COUNT(*) FROM activity_logs WHERE created_at >= CURRENT_DATE").Scan(&todayCount)

	byCategory := map[string]int{}
	rows, err := db.Query("SELECT category, COUNT(*) FROM activity_logs GROUP BY category")
	if err != nil {
		return total, todayCount, byCategory, err
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		var count int
		rows.Scan(&cat, &count)
		byCategory[cat] = count
	}
	return total, todayCount, byCategory, nil
}
