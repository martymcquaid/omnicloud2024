import { apiClient } from './client';

/** Reset database: clear all content, hashing progress, and torrents (keeps servers). */
export async function resetDb(): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>('/admin/db-reset');
  return data;
}
