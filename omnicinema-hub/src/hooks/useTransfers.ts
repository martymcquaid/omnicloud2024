import { useQuery } from '@tanstack/react-query';
import { transfersApi } from '@/api/transfers';
import { REFRESH_INTERVALS } from '@/utils/constants';

export const useTransfers = (params?: { server_id?: string; status?: string; torrent_id?: string }) =>
  useQuery({
    queryKey: ['transfers', params],
    queryFn: () => transfersApi.getAll(params),
    refetchInterval: REFRESH_INTERVALS.ACTIVE_TRANSFERS,
  });
