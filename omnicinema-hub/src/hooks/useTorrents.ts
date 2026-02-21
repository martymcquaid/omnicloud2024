import { useQuery } from '@tanstack/react-query';
import { torrentsApi } from '@/api/torrents';
import { REFRESH_INTERVALS } from '@/utils/constants';

export const useTorrents = (params?: { package_id?: string }) =>
  useQuery({
    queryKey: ['torrents', params],
    queryFn: () => torrentsApi.getAll(params),
    refetchInterval: REFRESH_INTERVALS.TORRENTS,
  });
