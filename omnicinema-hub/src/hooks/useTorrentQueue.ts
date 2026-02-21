import { useQuery } from '@tanstack/react-query';
import { torrentsApi } from '@/api/torrents';
import { REFRESH_INTERVALS } from '@/utils/constants';

export const useTorrentQueue = (params?: { server_id?: string; status?: string }) =>
  useQuery({
    queryKey: ['torrent-queue', params],
    queryFn: () => torrentsApi.getQueue(params),
    refetchInterval: REFRESH_INTERVALS.TORRENT_QUEUE,
  });
