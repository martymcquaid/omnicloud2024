import { apiClient } from './client';
import type { Server } from './types';

/** Label to show in UI: user-defined display name when set, otherwise device-reported name. */
export function serverDisplayName(s: Server): string {
  return (s.display_name?.trim() || s.name) || 'Unknown';
}

/** Backend returns { servers: Server[], count: number } */
type ServerListResponse = { servers?: Server[]; count?: number };

export interface ScanStatus {
  id?: string;
  server_id?: string;
  scan_type?: string;
  status: string;
  started_at?: string;
  completed_at?: string | null;
  packages_found?: number;
  packages_added?: number;
  packages_updated?: number;
  packages_removed?: number;
  errors?: string;
  message?: string;
}

export interface ActivityItem {
  category: string;
  action: string;
  title: string;
  detail?: string;
  progress?: number;
  speed?: string;
  speed_bytes?: number;
  eta_seconds?: number;
  extra?: Record<string, unknown>;
  started_at?: string;
}

export interface ServerActivityResponse {
  server_id: string;
  server_name: string;
  updated_at: string;
  activities: ActivityItem[];
}

export interface AllServerActivitiesResponse {
  servers: ServerActivityResponse[];
  count: number;
}

export const serversApi = {
  getAll: () =>
    apiClient.get<ServerListResponse>('/servers').then(r => (Array.isArray(r.data?.servers) ? r.data.servers : [])),
  getById: (id: string) => apiClient.get<Server>(`/servers/${id}`).then(r => r.data),
  register: (data: { name: string; location: string; api_url: string; mac_address: string; registration_key: string; storage_capacity_tb: number }) =>
    apiClient.post<{ id: string; message: string }>('/servers/register', data).then(r => r.data),
  heartbeat: (id: string) => apiClient.post(`/servers/${id}/heartbeat`).then(r => r.data),
  getDCPs: (id: string) => apiClient.get(`/servers/${id}/dcps`).then(r => r.data),
  rescan: (serverId: string) => apiClient.post(`/servers/${serverId}/rescan`),
  getScanStatus: (serverId: string) =>
    apiClient.get<ScanStatus>(`/servers/${serverId}/scan-status`).then(r => r.data),
  getAllActivities: () =>
    apiClient.get<AllServerActivitiesResponse>('/server-activities').then(r => r.data),
  getServerActivity: (serverId: string) =>
    apiClient.get<ServerActivityResponse>(`/servers/${serverId}/activities`).then(r => r.data),
};
