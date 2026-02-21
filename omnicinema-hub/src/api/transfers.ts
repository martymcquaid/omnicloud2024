import { apiClient } from './client';
import type { Transfer } from './types';

export const transfersApi = {
  getAll: (params?: { server_id?: string; status?: string; torrent_id?: string }) =>
    apiClient.get<Transfer[]>('/transfers', { params }).then(r => (Array.isArray(r.data) ? r.data : [])),
  getById: (id: string) => apiClient.get<Transfer>(`/transfers/${id}`).then(r => r.data),
  create: (data: { torrent_id: string; destination_server_id: string; requested_by: string; priority?: number }) =>
    apiClient.post<{ id: string; message: string }>('/transfers', data).then(r => r.data),
  update: (id: string, data: Partial<Transfer>) => apiClient.put(`/transfers/${id}`, data).then(r => r.data),
  cancel: ({ id, deleteData }: { id: string; deleteData: boolean }) =>
    apiClient.delete(`/transfers/${id}`, { params: { delete_data: deleteData } }).then(r => r.data),
  retry: (id: string) => apiClient.post(`/transfers/${id}/retry`).then(r => r.data),
  pause: (id: string) => apiClient.post(`/transfers/${id}/pause`).then(r => r.data),
  resume: (id: string) => apiClient.post(`/transfers/${id}/resume`).then(r => r.data),
};
