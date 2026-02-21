import { useQuery } from '@tanstack/react-query';
import { dcpsApi, type DCPListParams } from '@/api/dcps';
import { REFRESH_INTERVALS } from '@/utils/constants';

export const useDCPs = (params?: DCPListParams) =>
  useQuery({
    queryKey: ['dcps', params],
    queryFn: () => dcpsApi.getAll(params),
    refetchInterval: REFRESH_INTERVALS.DCPS,
  });
