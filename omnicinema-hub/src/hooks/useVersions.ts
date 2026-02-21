import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import { API_BASE_URL } from '@/api/client';

export interface Version {
  version: string;
  build_time: string;
  checksum: string;
  size_bytes: number;
  download_url: string;
  is_stable: boolean;
  release_notes?: string;
  created_at: string;
}

export function useVersions() {
  return useQuery({
    queryKey: ['versions'],
    queryFn: async () => {
      const response = await fetch(`${API_BASE_URL}/versions`);
      if (!response.ok) throw new Error('Failed to fetch versions');
      const data = await response.json();
      return data.versions as Version[];
    },
  });
}

export function useLatestVersion() {
  return useQuery({
    queryKey: ['versions', 'latest'],
    queryFn: async () => {
      const response = await fetch(`${API_BASE_URL}/versions/latest`);
      if (!response.ok) throw new Error('Failed to fetch latest version');
      return await response.json() as Version;
    },
  });
}

export function useTriggerUpgrade() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async ({ serverId, targetVersion }: { serverId: string; targetVersion: string }) => {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}/upgrade`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target_version: targetVersion }),
      });
      if (!response.ok) throw new Error('Failed to trigger upgrade');
      return await response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['servers'] });
    },
  });
}

export function useRestartServer() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (serverId: string) => {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}/restart`, {
        method: 'POST',
      });
      if (!response.ok) throw new Error('Failed to trigger restart');
      return await response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['servers'] });
    },
  });
}

export function useCreateBuild() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (versionName: string) => {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 5 * 60 * 1000); // 5 min for build
      const response = await fetch(`${API_BASE_URL}/builds`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ version_name: versionName || undefined }),
        signal: controller.signal,
      });
      clearTimeout(timeoutId);
      if (!response.ok) {
        const err = await response.json().catch(() => ({}));
        throw new Error((err as { message?: string }).message ?? (err as { error?: string }).error ?? 'Build failed');
      }
      return response.json();
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['versions'] });
      queryClient.invalidateQueries({ queryKey: ['versions', 'latest'] });
    },
    onError: (err: Error) => {
      toast.error(err.message || 'Build failed');
    },
  });
}
