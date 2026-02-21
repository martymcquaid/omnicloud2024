import axios from 'axios';

/** Single source for backend: set VITE_API_URL in .env (e.g. http://dcp1.omniplex.services:10858/api/v1) */
export const API_BASE_URL = (import.meta.env.VITE_API_URL ?? 'http://localhost:10858/api/v1').replace(/\/$/, '');

export const apiClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30000,
  headers: {
    'Content-Type': 'application/json',
  },
});

// Attach session token to every request
const TOKEN_KEY = 'omnicloud_session_token';
apiClient.interceptors.request.use((config) => {
  const token = localStorage.getItem(TOKEN_KEY);
  if (token) {
    config.headers = config.headers || {};
    config.headers['Authorization'] = `Bearer ${token}`;
  }
  return config;
});

// On 401 response, clear the token and redirect to login
apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error?.response?.status === 401 && !error?.config?.url?.includes('/auth/')) {
      localStorage.removeItem(TOKEN_KEY);
      // Only redirect if we're not already on the login page
      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }
    }
    return Promise.reject(error);
  },
);

// #region agent log
const DEBUG_LOG_ENDPOINT = 'http://localhost:7251/ingest/b617acab-fbea-4a4f-b976-910a5810283c';
const logDebug = (location: string, message: string, data: Record<string, unknown>, hypothesisId: string) => {
  fetch(DEBUG_LOG_ENDPOINT, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ location, message, data, timestamp: Date.now(), hypothesisId }) }).catch(() => {});
};
apiClient.interceptors.request.use((config) => {
  const url = (config.baseURL ?? '') + (config.url ?? '');
  logDebug('api/client.ts:request', 'API request', { url, baseURL: config.baseURL }, 'H1_H2_H5');
  return config;
});
apiClient.interceptors.response.use(
  (r) => r,
  (err) => {
    logDebug('api/client.ts:responseError', 'API error', {
      message: err?.message,
      code: err?.code,
      status: err?.response?.status,
      url: err?.config?.url,
      baseURL: err?.config?.baseURL,
    }, 'H1_H2_H4_H5');
    return Promise.reject(err);
  }
);
// #endregion
