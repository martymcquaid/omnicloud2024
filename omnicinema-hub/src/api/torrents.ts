import { apiClient } from './client';
import type { Torrent, Seeder, AnnounceAttempt, DCPPackage, TorrentQueueItem, TrackerLiveResponse, ServerTorrentStat } from './types';

export const torrentsApi = {
  getAll: async (params?: { package_id?: string; assetmap_uuid?: string }) => {
    try {
      // Fetch torrents and DCPs in parallel
      const [torrentsRes, dcpsRes] = await Promise.all([
        apiClient.get<Torrent[]>('/torrents', { params }),
        apiClient.get<{ dcps: DCPPackage[] }>('/dcps')
      ]);

      const torrents = Array.isArray(torrentsRes.data) ? torrentsRes.data : [];
      const dcps = dcpsRes.data?.dcps || [];
      
      // Create a map of package IDs to DCPs
      const dcpMap = dcps.reduce((acc, dcp) => {
        acc[dcp.id] = dcp;
        return acc;
      }, {} as Record<string, DCPPackage>);

      // Enrich torrents with DCP metadata
      return torrents.map(t => ({
        ...t,
        package_name: dcpMap[t.package_id]?.package_name || 'Unknown',
        content_title: dcpMap[t.package_id]?.content_title || '',
      }));
    } catch (error) {
      console.error('Failed to fetch torrents:', error);
      return [];
    }
  },
  
  getById: (infoHash: string) => apiClient.get<Torrent>(`/torrents/${infoHash}`).then(r => r.data),
  getSeeders: (infoHash: string) => apiClient.get<Seeder[]>(`/torrents/${infoHash}/seeders`).then(r => (Array.isArray(r.data) ? r.data : [])),
  getAnnounceAttempts: (infoHash: string, limit = 50) =>
    apiClient.get<AnnounceAttempt[]>(`/torrents/${infoHash}/announce-attempts`, { params: { limit } }).then(r => (Array.isArray(r.data) ? r.data : [])),
  getTrackerLive: () =>
    apiClient.get<TrackerLiveResponse>('/tracker/live').then(r => r.data),
  downloadFile: (infoHash: string) => apiClient.get(`/torrents/${infoHash}/file`, { responseType: 'blob' }),
  
  // Queue endpoints
  getQueue: (params?: { server_id?: string; status?: string }) =>
    apiClient.get<TorrentQueueItem[]>('/torrent-queue', { params }).then(r => (Array.isArray(r.data) ? r.data : [])),
  
  updateQueuePosition: (queueItemId: string, newPosition: number) =>
    apiClient.put(`/torrent-queue/${queueItemId}`, { new_position: newPosition }),
  
  retryQueueItem: (queueItemId: string) =>
    apiClient.post(`/torrent-queue/${queueItemId}/retry`),

  cancelQueueItem: (queueItemId: string) =>
    apiClient.post(`/torrent-queue/${queueItemId}/cancel`),

  clearCompletedQueue: () =>
    apiClient.post('/torrent-queue/clear-completed'),

  // Server torrent stats - detailed per-server torrent status
  getServerTorrentStats: (serverId: string) =>
    apiClient.get<ServerTorrentStat[]>(`/servers/${serverId}/torrent-stats`)
      .then(r => (Array.isArray(r.data) ? r.data : [])),

  getAllServersTorrentStats: () =>
    apiClient.get<ServerTorrentStat[]>('/torrent-stats/all')
      .then(r => (Array.isArray(r.data) ? r.data : [])),
};
