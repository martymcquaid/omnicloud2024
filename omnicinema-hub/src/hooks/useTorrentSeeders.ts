import { useQuery } from '@tanstack/react-query';
import { torrentsApi } from '@/api/torrents';
import { REFRESH_INTERVALS } from '@/utils/constants';

export const useTorrentSeeders = (infoHash: string) =>
  useQuery({
    queryKey: ['torrent-seeders', infoHash],
    queryFn: () => torrentsApi.getSeeders(infoHash),
    refetchInterval: REFRESH_INTERVALS.TORRENT_SEEDERS || 30000,
    enabled: !!infoHash,
  });
