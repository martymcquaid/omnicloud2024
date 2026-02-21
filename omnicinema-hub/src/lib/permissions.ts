/** All pages available in the system, mapped to display labels */
export const ALL_PAGES = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'dcps', label: 'Content' },
  { id: 'servers', label: 'Sites' },
  { id: 'transfers', label: 'Transfers' },
  { id: 'torrents', label: 'Ingesting' },
  { id: 'torrent-status', label: 'Transfer Status' },
  { id: 'tracker', label: 'Tracker' },
  { id: 'analytics', label: 'Analytics' },
  { id: 'settings', label: 'Settings' },
] as const;

/** Map route paths to page permission IDs */
export const ROUTE_TO_PAGE: Record<string, string> = {
  '/dcps': 'dcps',
  '/servers': 'servers',
  '/sites': 'servers',
  '/transfers': 'transfers',
  '/torrents': 'torrents',
  '/torrent-status': 'torrent-status',
  '/tracker': 'tracker',
  '/analytics': 'analytics',
  '/settings': 'settings',
};

/** Display-friendly role names */
export const ROLE_LABELS: Record<string, string> = {
  admin: 'Administrator',
  it: 'IT',
  manager: 'Manager',
};
