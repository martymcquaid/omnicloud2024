import { apiClient } from './client';

export interface UserRecord {
  id: string;
  username: string;
  role: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface CreateUserPayload {
  username: string;
  password: string;
  role: string;
}

export interface UpdateUserPayload {
  username: string;
  role: string;
  is_active?: boolean;
}

export interface RolePermission {
  role: string;
  allowed_pages: string[];
  description: string;
}

export interface UpdateRolePermissionsPayload {
  allowed_pages: string[];
  description: string;
}

export const usersApi = {
  list: async (): Promise<UserRecord[]> => {
    const { data } = await apiClient.get('/users');
    return data;
  },

  create: async (payload: CreateUserPayload): Promise<UserRecord> => {
    const { data } = await apiClient.post('/users', payload);
    return data;
  },

  update: async (id: string, payload: UpdateUserPayload): Promise<void> => {
    await apiClient.put(`/users/${id}`, payload);
  },

  changePassword: async (id: string, password: string): Promise<void> => {
    await apiClient.put(`/users/${id}/password`, { password });
  },

  delete: async (id: string): Promise<void> => {
    await apiClient.delete(`/users/${id}`);
  },

  listRoles: async (): Promise<RolePermission[]> => {
    const { data } = await apiClient.get('/roles');
    return data;
  },

  updateRolePermissions: async (role: string, payload: UpdateRolePermissionsPayload): Promise<void> => {
    await apiClient.put(`/roles/${role}/permissions`, payload);
  },
};
