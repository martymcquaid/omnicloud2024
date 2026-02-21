// API Types for OmniCloud

export interface Server {
  id: string;
  name: string;
  /** User-defined label; when set, use this instead of name in the UI. Never overwritten by device sync. */
  display_name?: string;
  location: string;
  api_url: string;
  mac_address: string;
  is_authorized: boolean;
  last_seen: string;
  storage_capacity_tb: number;
  created_at: string;
  updated_at?: string;
  software_version?: string;
  upgrade_status?: 'idle' | 'pending' | 'upgrading' | 'success' | 'failed';
  target_version?: string;
  upgrade_progress_message?: string;
  upgrade_status_updated_at?: string;
}

export interface DCPPackage {
  id: string;
  assetmap_uuid: string;
  package_name: string;
  content_title: string;
  content_kind: string;
  issue_date?: string;
  issuer: string;
  creator: string;
  volume_count: number;
  total_size_bytes: number;
  file_count: number;
  discovered_at: string;
  last_verified?: string;
}

export interface ServerTorrentStatus {
  server_id: string;
  server_name?: string;
  status: string;
  error_message?: string;
}

export interface Torrent {
  id: string;
  package_id: string;
  package_name?: string;
  content_title?: string;
  info_hash: string;
  piece_size: number;
  total_pieces: number;
  file_count: number;
  total_size_bytes: number;
  created_by_server_id: string;
  created_at: string;
  seeders_count?: number;
  server_statuses?: ServerTorrentStatus[];
}

export interface Seeder {
  id: string;
  torrent_id: string;
  server_id: string;
  server_name?: string;
  local_path: string;
  status: 'seeding' | 'paused' | 'error';
  uploaded_bytes: number;
  last_announce: string;
}

export interface AnnounceAttempt {
  info_hash: string;
  peer_id: string;
  ip: string;
  port: number;
  event: string;
  status: 'ok' | 'error' | string;
  failure_reason?: string;
  created_at: string;
}

export interface TrackerPeerSnapshot {
  peer_id: string;
  ip: string;
  port: number;
  uploaded: number;
  downloaded: number;
  left: number;
  last_seen: string;
  is_seeder: boolean;
}

export interface TrackerSwarmSnapshot {
  info_hash: string;
  seeders: number;
  leechers: number;
  peers_count: number;
  last_announce?: string;
  peers: TrackerPeerSnapshot[];
}

export interface TrackerLiveTorrent {
  id: string;
  package_id: string;
  package_name?: string;
  content_title?: string;
  info_hash: string;
  created_at: string;
  active: boolean;
  seeders: number;
  leechers: number;
  peers_count: number;
  last_announce?: string;
  recent_attempts_15m: number;
  recent_error_attempts_15m: number;
  last_attempt_at?: string;
}

export interface TrackerLiveResponse {
  tracker_available: boolean;
  interval_sec: number;
  active_swarms: number;
  total_live_peers: number;
  total_torrents: number;
  generated_at: string;
  active_swarm_peers: TrackerSwarmSnapshot[];
  torrents: TrackerLiveTorrent[];
}

export interface Transfer {
  id: string;
  torrent_id: string;
  package_id?: string;
  package_name?: string;
  source_server_id?: string;
  source_server_name?: string;
  destination_server_id: string;
  destination_server_name?: string;
  requested_by: string;
  status: 'queued' | 'checking' | 'downloading' | 'paused' | 'completed' | 'failed' | 'error' | 'cancelled';
  priority: number;
  progress_percent: number;
  downloaded_bytes: number;
  total_size_bytes?: number;
  download_speed_bps: number;
  upload_speed_bps: number;
  peers_connected: number;
  eta_seconds?: number;
  error_message?: string;
  started_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface TorrentQueueItem {
  id: string;
  package_id: string;
  package_name?: string;
  server_id: string;
  server_name?: string;
  status: 'queued' | 'generating' | 'completed' | 'failed';
  progress_percent: number;
  current_file?: string;
  error_message?: string;
  queue_position?: number;
  queued_at: string;
  started_at?: string;
  completed_at?: string;
  eta_seconds?: number;
  hashing_speed_bps?: number;
  total_size_bytes?: number;
  bytes_hashed?: number;
  total_pieces?: number;
  pieces_hashed?: number;
}

export interface ServerTorrentStat {
  server_id: string;
  server_name: string;
  info_hash: string;
  package_id: string;
  package_name: string;
  content_title?: string;
  status: 'verifying' | 'seeding' | 'downloading' | 'paused' | 'error' | 'idle' | 'completed';
  is_loaded: boolean;
  is_seeding: boolean;
  is_downloading: boolean;
  bytes_completed: number;
  bytes_total: number;
  progress_percent: number;
  pieces_completed: number;
  pieces_total: number;
  download_speed_bps: number;
  upload_speed_bps: number;
  uploaded_bytes: number;
  peers_connected: number;
  eta_seconds?: number;
  announced_to_tracker: boolean;
  last_announce_attempt?: string;
  last_announce_success?: string;
  announce_error?: string;
  updated_at: string;
}

export interface PackageServerStatus {
  server_id: string;
  server_name: string;
  status: 'seeding' | 'downloading' | 'paused' | 'checking' | 'queued' | 'incomplete' | 'error' | 'complete' | 'missing';
  progress_percent: number;
  downloaded_bytes: number;
  total_size_bytes: number;
  download_speed_bps: number;
  peers_connected: number;
  eta_seconds?: number;
  error_message?: string;
  transfer_id?: string;
  has_local_data: boolean;
  is_seeding: boolean;
  last_seen?: string;
  ingestion_status?: string;
  rosettabridge_path?: string;
  download_path?: string;
}

export interface HealthResponse {
  status: string;
  time: string;
  version: string;
}
