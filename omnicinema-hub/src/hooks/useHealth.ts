import { useQuery } from '@tanstack/react-query';
import { healthApi } from '@/api/health';
import { REFRESH_INTERVALS } from '@/utils/constants';

export const useHealth = () =>
  useQuery({
    queryKey: ['health'],
    queryFn: healthApi.check,
    refetchInterval: REFRESH_INTERVALS.HEALTH,
  });
