import { apiClient } from './client';

export interface LoginResponse {
  token: string;
  username: string;
  role: string;
  allowed_pages: string[];
  expires_at: string;
}

export interface SessionResponse {
  authenticated: boolean;
  username?: string;
  role?: string;
  allowed_pages?: string[];
  expires_at?: string;
}

export const authApi = {
  login: async (username: string, password: string): Promise<LoginResponse> => {
    const { data } = await apiClient.post('/auth/login', { username, password });
    return data;
  },

  logout: async (): Promise<void> => {
    await apiClient.post('/auth/logout');
  },

  checkSession: async (): Promise<SessionResponse> => {
    const { data } = await apiClient.get('/auth/session');
    return data;
  },
};
