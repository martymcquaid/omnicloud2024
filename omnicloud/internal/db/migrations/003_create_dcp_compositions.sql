-- Create dcp_compositions table for CPL (Composition Playlist) data
CREATE TABLE IF NOT EXISTS dcp_compositions (
    id UUID PRIMARY KEY,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    cpl_uuid UUID NOT NULL,
    content_title_text VARCHAR(512),
    full_content_title VARCHAR(512),
    content_kind VARCHAR(50),
    content_version_id UUID,
    label_text VARCHAR(255),
    issue_date TIMESTAMP WITH TIME ZONE,
    issuer VARCHAR(255),
    creator VARCHAR(255),
    edit_rate VARCHAR(50),
    frame_rate VARCHAR(50),
    screen_aspect_ratio VARCHAR(50),
    resolution_width INTEGER,
    resolution_height INTEGER,
    main_sound_configuration VARCHAR(255),
    main_sound_sample_rate VARCHAR(50),
    luminance INTEGER,
    release_territory VARCHAR(10),
    distributor VARCHAR(255),
    facility VARCHAR(255),
    reel_count INTEGER,
    total_duration_frames INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_package_id ON dcp_compositions(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_cpl_uuid ON dcp_compositions(cpl_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_content_kind ON dcp_compositions(content_kind);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_release_territory ON dcp_compositions(release_territory);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_resolution ON dcp_compositions(resolution_width, resolution_height);

-- Trigger for updated_at
CREATE TRIGGER update_dcp_compositions_updated_at BEFORE UPDATE ON dcp_compositions
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create dcp_reels table for individual reels from CPL
CREATE TABLE IF NOT EXISTS dcp_reels (
    id UUID PRIMARY KEY,
    composition_id UUID NOT NULL REFERENCES dcp_compositions(id) ON DELETE CASCADE,
    reel_uuid UUID NOT NULL,
    reel_number INTEGER NOT NULL,
    duration_frames INTEGER,
    picture_asset_uuid UUID,
    picture_edit_rate VARCHAR(50),
    picture_entry_point INTEGER,
    picture_intrinsic_duration INTEGER,
    picture_key_id UUID,
    picture_hash VARCHAR(255),
    sound_asset_uuid UUID,
    sound_configuration VARCHAR(255),
    subtitle_asset_uuid UUID,
    subtitle_language VARCHAR(10),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_dcp_reels_composition_id ON dcp_reels(composition_id);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_reel_uuid ON dcp_reels(reel_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_reel_number ON dcp_reels(reel_number);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_picture_asset_uuid ON dcp_reels(picture_asset_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_sound_asset_uuid ON dcp_reels(sound_asset_uuid);
