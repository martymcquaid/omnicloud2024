import { apiClient } from './client';

export interface ActivityLog {
  id: string;
  user_id: string;
  username: string;
  action: string;
  category: string;
  resource_type: string;
  resource_id: string;
  resource_name: string;
  details: string;
  ip_address: string;
  status: string;
  created_at: string;
}

export interface ActivityLogFilter {
  category?: string;
  action?: string;
  search?: string;
  start?: string;
  end?: string;
  limit?: number;
  offset?: number;
}

export interface ActivityLogResponse {
  logs: ActivityLog[];
  total: number;
  limit: number;
  offset: number;
}

export interface ActivityLogStats {
  total: number;
  today_count: number;
  by_category: Record<string, number>;
}

export const activityLogsApi = {
  list: async (filter: ActivityLogFilter = {}): Promise<ActivityLogResponse> => {
    const params = new URLSearchParams();
    if (filter.category) params.set('category', filter.category);
    if (filter.action) params.set('action', filter.action);
    if (filter.search) params.set('search', filter.search);
    if (filter.start) params.set('start', filter.start);
    if (filter.end) params.set('end', filter.end);
    params.set('limit', String(filter.limit ?? 50));
    params.set('offset', String(filter.offset ?? 0));
    const { data } = await apiClient.get(`/activity-logs?${params.toString()}`);
    return data;
  },

  stats: async (): Promise<ActivityLogStats> => {
    const { data } = await apiClient.get('/activity-logs/stats');
    return data;
  },
};
