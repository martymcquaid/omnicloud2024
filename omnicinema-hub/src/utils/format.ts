export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

export function formatSpeed(bytesPerSecond: number): string {
  if (bytesPerSecond === 0) return '—';
  // Convert bytes per second to megabits per second (mbps)
  // 1 byte = 8 bits, so multiply by 8 and divide by 1024*1024 to get mbps
  const mbps = (bytesPerSecond * 8) / (1024 * 1024);
  if (mbps < 1) {
    // For speeds less than 1 mbps, show in kbps
    const kbps = (bytesPerSecond * 8) / 1024;
    return `${kbps.toFixed(1)} kbps`;
  }
  return `${mbps.toFixed(1)} mbps`;
}

export function formatETA(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return '—';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

export function formatRelativeTime(timestamp: string | null | undefined): string {
  if (timestamp == null || timestamp === '') return '—';
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  if (Number.isNaN(then)) return '—';
  const diff = Math.floor((now - then) / 1000);
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  return `${Math.floor(diff / 86400)}d ago`;
}

/** Duration from timestamp to now, e.g. "6m", "2h", "3d" (no "ago"). */
export function formatDurationSince(timestamp: string | null | undefined): string {
  if (timestamp == null || timestamp === '') return '—';
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  if (Number.isNaN(then)) return '—';
  const diff = Math.floor((now - then) / 1000);
  if (diff < 60) return `${diff}s`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h`;
  return `${Math.floor(diff / 86400)}d`;
}

export function isServerOnline(lastSeen: string | null | undefined): boolean {
  if (lastSeen == null || lastSeen === '') return false;
  const then = new Date(lastSeen).getTime();
  if (Number.isNaN(then)) return false;
  const diff = Date.now() - then;
  return diff < 5 * 60 * 1000; // 5 minutes
}

export function getStatusColor(status: string): string {
  switch (status) {
    case 'queued': return 'info';
    case 'downloading': case 'generating': return 'primary';
    case 'completed': case 'seeding': return 'success';
    case 'failed': case 'error': return 'destructive';
    case 'cancelled': case 'paused': return 'muted';
    default: return 'muted';
  }
}
