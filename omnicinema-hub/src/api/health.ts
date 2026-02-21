import { apiClient } from './client';
import type { HealthResponse } from './types';

// #region agent log
const DEBUG_LOG_ENDPOINT = 'http://localhost:7251/ingest/b617acab-fbea-4a4f-b976-910a5810283c';
const logDebug = (location: string, message: string, data: Record<string, unknown>, hypothesisId: string) => {
  fetch(DEBUG_LOG_ENDPOINT, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ location, message, data, timestamp: Date.now(), hypothesisId }) }).catch(() => {});
};
// #endregion

export const healthApi = {
  check: () =>
    apiClient.get<HealthResponse>('/health').then((r) => {
      // #region agent log
      logDebug('api/health.ts:check', 'health success', { status: r.data?.status, data: r.data }, 'H3_H4');
      // #endregion
      return r.data;
    }).catch((e) => {
      // #region agent log
      logDebug('api/health.ts:check', 'health failed', { message: e?.message, code: e?.code }, 'H1_H2_H4');
      // #endregion
      throw e;
    }),
};
