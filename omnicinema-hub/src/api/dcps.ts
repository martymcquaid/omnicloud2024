import { apiClient } from './client';
import type { DCPPackage, PackageServerStatus } from './types';

export interface DCPListParams {
  search?: string;
  content_kind?: string;
  server_ids?: string[];  // filter to DCPs present on these servers
  limit?: number;
  offset?: number;
}

export interface DCPListResponse {
  dcps: DCPPackage[];
  count: number;
  total_count: number;
  offset: number;
  limit: number;
}

export const dcpsApi = {
  getAll: (params?: DCPListParams): Promise<DCPListResponse> => {
    const { server_ids, ...rest } = params ?? {};
    const serialised: Record<string, unknown> = { ...rest };
    if (server_ids && server_ids.length > 0) {
      serialised.server_ids = server_ids.join(',');
    }
    return apiClient.get<DCPListResponse>('/dcps', { params: serialised }).then(r => ({
      dcps: Array.isArray(r.data?.dcps) ? r.data.dcps : [],
      count: r.data?.count ?? 0,
      total_count: r.data?.total_count ?? 0,
      offset: r.data?.offset ?? 0,
      limit: r.data?.limit ?? 0,
    }));
  },
  getById: (uuid: string) => apiClient.get<DCPPackage>(`/dcps/${uuid}`).then(r => r.data),
  getPackageServerStatus: (packageId: string) =>
    apiClient.get<PackageServerStatus[]>(`/packages/${packageId}/server-status`).then(r => Array.isArray(r.data) ? r.data : []),
  deleteContent: (data: { package_id: string; server_id: string; command: string; target_path?: string }) =>
    apiClient.post('/content-commands', data).then(r => r.data),
  deleteContentWS: (serverId: string, data: { package_id: string; target_path?: string }) =>
    apiClient.post<{ success: boolean; message: string; error?: string }>(`/servers/${serverId}/delete-content`, data).then(r => r.data),
};
