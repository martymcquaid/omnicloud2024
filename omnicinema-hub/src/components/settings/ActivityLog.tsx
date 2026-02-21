import { useState, useEffect, useCallback } from 'react';
import { activityLogsApi, ActivityLog as ActivityLogEntry, ActivityLogFilter, ActivityLogStats } from '@/api/activityLogs';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { toast } from 'sonner';
import {
  Loader2,
  LogIn,
  LogOut,
  ShieldAlert,
  UserPlus,
  UserCog,
  Key,
  Trash2,
  ShieldCheck,
  ArrowLeftRight,
  Pause,
  Play,
  RotateCcw,
  Server,
  Upload,
  ScanSearch,
  Database,
  Settings,
  Package,
  ChevronDown,
  ChevronRight,
  RefreshCw,
  Search,
  X,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

// --- Action label mapping ---
interface ActionMeta {
  label: string;
  icon: LucideIcon;
}

const ACTION_META: Record<string, ActionMeta> = {
  'user.login': { label: 'Logged in', icon: LogIn },
  'user.login_failed': { label: 'Failed login', icon: ShieldAlert },
  'user.logout': { label: 'Logged out', icon: LogOut },
  'user.create': { label: 'Created user', icon: UserPlus },
  'user.update': { label: 'Updated user', icon: UserCog },
  'user.password_change': { label: 'Changed password', icon: Key },
  'user.delete': { label: 'Deleted user', icon: Trash2 },
  'role.update': { label: 'Updated role permissions', icon: ShieldCheck },
  'transfer.create': { label: 'Created transfer', icon: ArrowLeftRight },
  'transfer.delete': { label: 'Deleted transfer', icon: Trash2 },
  'transfer.pause': { label: 'Paused transfer', icon: Pause },
  'transfer.resume': { label: 'Resumed transfer', icon: Play },
  'transfer.retry': { label: 'Retried transfer', icon: RotateCcw },
  'server.update': { label: 'Updated server', icon: Server },
  'server.delete': { label: 'Deleted server', icon: Trash2 },
  'server.restart': { label: 'Restarted server', icon: RotateCcw },
  'server.upgrade': { label: 'Triggered upgrade', icon: Upload },
  'server.rescan': { label: 'Triggered rescan', icon: ScanSearch },
  'settings.update': { label: 'Updated settings', icon: Settings },
  'system.db_reset': { label: 'Reset database', icon: Database },
  'queue.retry': { label: 'Retried queue item', icon: RotateCcw },
  'queue.cancel': { label: 'Cancelled queue item', icon: X },
  'queue.clear': { label: 'Cleared completed queue', icon: Trash2 },
  'content.command': { label: 'Content command', icon: Package },
  'content.delete': { label: 'Deleted content', icon: Trash2 },
};

// --- Category styling ---
const CATEGORY_STYLES: Record<string, string> = {
  auth: 'bg-purple-100 text-purple-700',
  users: 'bg-blue-100 text-blue-700',
  transfers: 'bg-cyan-100 text-cyan-700',
  servers: 'bg-green-100 text-green-700',
  content: 'bg-orange-100 text-orange-700',
  system: 'bg-red-100 text-red-700',
  torrents: 'bg-amber-100 text-amber-700',
};

const CATEGORY_LABELS: Record<string, string> = {
  auth: 'Auth',
  users: 'Users',
  transfers: 'Transfers',
  servers: 'Servers',
  content: 'Content',
  system: 'System',
  torrents: 'Torrents',
};

const CATEGORIES = ['auth', 'users', 'transfers', 'servers', 'content', 'system', 'torrents'];

// --- Relative time helper ---
function timeAgo(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diff = Math.floor((now - then) / 1000);
  if (diff < 5) return 'just now';
  if (diff < 60) return `${diff}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 604800) return `${Math.floor(diff / 86400)}d ago`;
  return new Date(dateStr).toLocaleDateString('en-GB');
}

const PAGE_SIZE = 50;

export default function ActivityLog() {
  const [logs, setLogs] = useState<ActivityLogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [stats, setStats] = useState<ActivityLogStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [offset, setOffset] = useState(0);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  // Filters
  const [category, setCategory] = useState('');
  const [search, setSearch] = useState('');
  const [searchInput, setSearchInput] = useState('');
  const [startDate, setStartDate] = useState('');
  const [endDate, setEndDate] = useState('');

  const fetchLogs = useCallback(async (newOffset: number, append: boolean) => {
    if (append) setLoadingMore(true); else setLoading(true);
    try {
      const filter: ActivityLogFilter = {
        limit: PAGE_SIZE,
        offset: newOffset,
      };
      if (category) filter.category = category;
      if (search) filter.search = search;
      if (startDate) filter.start = startDate;
      if (endDate) filter.end = endDate;
      const res = await activityLogsApi.list(filter);
      const newLogs = Array.isArray(res.logs) ? res.logs : [];
      if (append) {
        setLogs(prev => [...prev, ...newLogs]);
      } else {
        setLogs(newLogs);
      }
      setTotal(res.total);
      setOffset(newOffset);
    } catch {
      toast.error('Failed to load activity logs');
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, [category, search, startDate, endDate]);

  const fetchStats = useCallback(async () => {
    try {
      const data = await activityLogsApi.stats();
      setStats(data);
    } catch {
      // stats are non-critical
    }
  }, []);

  // Initial load + auto-refresh
  useEffect(() => {
    fetchLogs(0, false);
    fetchStats();
    const interval = setInterval(() => {
      if (offset === 0) {
        fetchLogs(0, false);
        fetchStats();
      }
    }, 10000);
    return () => clearInterval(interval);
  }, [fetchLogs, fetchStats, offset]);

  // Debounced search
  useEffect(() => {
    const timer = setTimeout(() => setSearch(searchInput), 300);
    return () => clearTimeout(timer);
  }, [searchInput]);

  // Reset offset when filters change
  useEffect(() => {
    setOffset(0);
  }, [category, search, startDate, endDate]);

  const handleLoadMore = () => {
    fetchLogs(offset + PAGE_SIZE, true);
  };

  const clearFilters = () => {
    setCategory('');
    setSearchInput('');
    setSearch('');
    setStartDate('');
    setEndDate('');
  };

  const hasFilters = category || search || startDate || endDate;

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-lg font-semibold text-gray-900">Activity Log</h2>
          <p className="text-sm text-gray-500">Track all user actions and system events</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => { fetchLogs(0, false); fetchStats(); }}>
          <RefreshCw className="h-4 w-4 mr-1" /> Refresh
        </Button>
      </div>

      {/* Stats cards */}
      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
          <div className="border rounded-lg p-3">
            <div className="text-2xl font-bold text-gray-900">{stats.total.toLocaleString()}</div>
            <div className="text-xs text-gray-500">Total Events</div>
          </div>
          <div className="border rounded-lg p-3">
            <div className="text-2xl font-bold text-blue-600">{stats.today_count.toLocaleString()}</div>
            <div className="text-xs text-gray-500">Today</div>
          </div>
          {CATEGORIES.filter(c => stats.by_category[c]).map(c => (
            <div key={c} className="border rounded-lg p-3">
              <div className="text-2xl font-bold text-gray-900">{stats.by_category[c].toLocaleString()}</div>
              <div className="text-xs text-gray-500">{CATEGORY_LABELS[c]}</div>
            </div>
          ))}
        </div>
      )}

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-2 mb-4">
        <Select value={category} onValueChange={v => setCategory(v === 'all' ? '' : v)}>
          <SelectTrigger className="w-[150px]">
            <SelectValue placeholder="All categories" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All categories</SelectItem>
            {CATEGORIES.map(c => (
              <SelectItem key={c} value={c}>{CATEGORY_LABELS[c]}</SelectItem>
            ))}
          </SelectContent>
        </Select>

        <div className="relative flex-1 min-w-[200px] max-w-[300px]">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-gray-400" />
          <Input
            placeholder="Search users, actions..."
            value={searchInput}
            onChange={e => setSearchInput(e.target.value)}
            className="pl-9"
          />
        </div>

        <Input
          type="date"
          value={startDate}
          onChange={e => setStartDate(e.target.value)}
          className="w-[150px]"
          placeholder="Start date"
        />
        <Input
          type="date"
          value={endDate}
          onChange={e => setEndDate(e.target.value)}
          className="w-[150px]"
          placeholder="End date"
        />

        {hasFilters && (
          <Button variant="ghost" size="sm" onClick={clearFilters}>
            <X className="h-4 w-4 mr-1" /> Clear
          </Button>
        )}

        <div className="ml-auto text-xs text-gray-500">
          {total.toLocaleString()} event{total !== 1 ? 's' : ''}
        </div>
      </div>

      {/* Table */}
      {loading ? (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="h-6 w-6 animate-spin text-gray-400" />
        </div>
      ) : logs.length === 0 ? (
        <div className="text-center py-12 text-gray-500">
          <p className="text-sm">{hasFilters ? 'No events match your filters' : 'No activity logged yet'}</p>
        </div>
      ) : (
        <>
          <div className="border rounded-lg overflow-hidden">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8"></TableHead>
                  <TableHead className="w-[100px]">Time</TableHead>
                  <TableHead className="w-[100px]">User</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Resource</TableHead>
                  <TableHead className="w-[80px]">Status</TableHead>
                  <TableHead className="w-[110px]">IP</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {logs.map(log => {
                  const meta = ACTION_META[log.action];
                  const Icon = meta?.icon;
                  const isExpanded = expandedId === log.id;
                  const catStyle = CATEGORY_STYLES[log.category] ?? 'bg-gray-100 text-gray-700';

                  return (
                    <>
                      <TableRow
                        key={log.id}
                        className="cursor-pointer hover:bg-gray-50/60 transition-colors"
                        onClick={() => setExpandedId(isExpanded ? null : log.id)}
                      >
                        <TableCell className="px-2">
                          {isExpanded ? <ChevronDown className="h-4 w-4 text-gray-400" /> : <ChevronRight className="h-4 w-4 text-gray-400" />}
                        </TableCell>
                        <TableCell className="text-xs text-gray-500 whitespace-nowrap" title={new Date(log.created_at).toLocaleString()}>
                          {timeAgo(log.created_at)}
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center gap-1.5">
                            <div className="w-6 h-6 rounded-full bg-gradient-to-br from-blue-500 to-blue-400 flex items-center justify-center text-white text-[10px] font-bold shrink-0">
                              {log.username.charAt(0).toUpperCase()}
                            </div>
                            <span className="text-sm font-medium truncate max-w-[80px]">{log.username}</span>
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center gap-2">
                            {Icon && <Icon className="h-4 w-4 text-gray-500 shrink-0" />}
                            <span className="text-sm">{meta?.label ?? log.action}</span>
                            <Badge variant="outline" className={`text-[10px] px-1.5 py-0 ${catStyle}`}>
                              {CATEGORY_LABELS[log.category] ?? log.category}
                            </Badge>
                          </div>
                        </TableCell>
                        <TableCell className="text-sm text-gray-600 truncate max-w-[200px]">
                          {log.resource_name || log.resource_id || '-'}
                        </TableCell>
                        <TableCell>
                          <Badge variant={log.status === 'success' ? 'default' : 'destructive'}
                            className={log.status === 'success' ? 'bg-green-600 text-[10px]' : 'text-[10px]'}>
                            {log.status}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-xs text-gray-500 font-mono">
                          {log.ip_address || '-'}
                        </TableCell>
                      </TableRow>
                      {isExpanded && (
                        <TableRow key={`${log.id}-detail`}>
                          <TableCell colSpan={7} className="bg-gray-50 p-4">
                            <div className="grid grid-cols-2 gap-x-8 gap-y-2 text-sm">
                              <div><span className="font-medium text-gray-500">Event ID:</span> <span className="font-mono text-xs">{log.id}</span></div>
                              <div><span className="font-medium text-gray-500">Timestamp:</span> {new Date(log.created_at).toLocaleString()}</div>
                              <div><span className="font-medium text-gray-500">Action:</span> {log.action}</div>
                              <div><span className="font-medium text-gray-500">Category:</span> {log.category}</div>
                              <div><span className="font-medium text-gray-500">Resource Type:</span> {log.resource_type || '-'}</div>
                              <div><span className="font-medium text-gray-500">Resource ID:</span> <span className="font-mono text-xs">{log.resource_id || '-'}</span></div>
                              {log.resource_name && <div className="col-span-2"><span className="font-medium text-gray-500">Resource Name:</span> {log.resource_name}</div>}
                              {log.details && (
                                <div className="col-span-2">
                                  <span className="font-medium text-gray-500">Details:</span>
                                  <pre className="mt-1 text-xs bg-white border rounded p-2 overflow-x-auto">{(() => {
                                    try { return JSON.stringify(JSON.parse(log.details), null, 2); } catch { return log.details; }
                                  })()}</pre>
                                </div>
                              )}
                            </div>
                          </TableCell>
                        </TableRow>
                      )}
                    </>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          {/* Pagination */}
          {logs.length < total && (
            <div className="flex items-center justify-center mt-4 gap-3">
              <span className="text-sm text-gray-500">
                Showing {logs.length} of {total.toLocaleString()}
              </span>
              <Button variant="outline" size="sm" onClick={handleLoadMore} disabled={loadingMore}>
                {loadingMore ? <><Loader2 className="h-4 w-4 animate-spin mr-1" /> Loading...</> : 'Load more'}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
