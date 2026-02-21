import { useQuery } from '@tanstack/react-query';
import { serversApi } from '@/api/servers';
import { REFRESH_INTERVALS } from '@/utils/constants';
import type { Server } from '@/api/types';

/** When true, poll every 5s while any server has upgrade pending/upgrading so progress is visible. */
export const useServers = (options?: { pollWhenUpgradePending?: boolean }) =>
  useQuery({
    queryKey: ['servers'],
    queryFn: serversApi.getAll,
    refetchInterval: (query) => {
      if (options?.pollWhenUpgradePending) {
        const list = (query.state.data as Server[] | undefined) ?? [];
        const hasPending = list.some(
          (s) => s.upgrade_status === 'pending' || s.upgrade_status === 'upgrading'
        );
        if (hasPending) return 5000;
      }
      return REFRESH_INTERVALS.SERVERS;
    },
  });

/** Polls all server activities every 5s for live dashboard */
export const useServerActivities = () =>
  useQuery({
    queryKey: ['server-activities'],
    queryFn: serversApi.getAllActivities,
    refetchInterval: 5000,
  });
