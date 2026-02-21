import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import { useTransfers } from '@/hooks/useTransfers';
import { useServers } from '@/hooks/useServers';
import { formatBytes, formatSpeed, formatETA, formatRelativeTime } from '@/utils/format';
import { StatusBadge } from '@/components/common/StatusIndicator';
import { Zap, History, AlertTriangle, ChevronDown, ChevronRight, X, RotateCcw, Trash2, FolderOpen, Pause, Play, Activity, Search, Download, CheckCircle2, XCircle, Clock, Filter } from 'lucide-react';
import { cn } from '@/lib/utils';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { transfersApi } from '@/api/transfers';
import { serverDisplayName } from '@/api/servers';
import type { Transfer } from '@/api/types';

type Tab = 'active' | 'completed' | 'history';

interface CancelDialogState {
  open: boolean;
  transfer: Transfer | null;
  serverName: string;
}

function isStuck(t: Transfer): boolean {
  if (t.status !== 'downloading') return false;
  if (t.download_speed_bps > 0 || t.peers_connected > 0) return false;
  if (!t.updated_at) return false;
  const lastUpdate = new Date(t.updated_at).getTime();
  return (Date.now() - lastUpdate) > 5 * 60 * 1000;
}

function isHealthy(t: Transfer): boolean {
  if (t.status !== 'downloading') return false;
  return t.download_speed_bps > 1024 * 1024 * 10 && t.peers_connected > 0; // 10 MB/s+
}

export default function Transfers() {
  const [tab, setTab] = useState<Tab>('active');
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [serverFilter, setServerFilter] = useState<string>('all');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const { data: transfersRaw, isLoading } = useTransfers();
  const { data: serversRaw } = useServers();
  const queryClient = useQueryClient();

  const transfers = Array.isArray(transfersRaw) ? transfersRaw : [];
  const servers = Array.isArray(serversRaw) ? serversRaw : [];

  // Speed history tracking: rolling average + zero-since timestamp per transfer
  interface SpeedEntry { samples: number[]; zeroSince: number | null; }
  const speedHistory = useRef<Record<string, SpeedEntry>>({});
  const [, forceSpeedRender] = useState(0);

  useEffect(() => {
    const now = Date.now();
    transfers.forEach(t => {
      const speed = t.download_speed_bps ?? 0;
      const prev = speedHistory.current[t.id] ?? { samples: [], zeroSince: null };
      const samples = [...prev.samples, speed].slice(-6); // keep last 6 samples (30s)
      const zeroSince = speed > 0 ? null : (prev.zeroSince ?? now);
      speedHistory.current[t.id] = { samples, zeroSince };
    });
    forceSpeedRender(n => n + 1);
  }, [transfersRaw]); // eslint-disable-line react-hooks/exhaustive-deps

  const getSpeedInfo = (t: Transfer) => {
    const entry = speedHistory.current[t.id];
    if (!entry || entry.samples.length === 0) return { avgSpeed: t.download_speed_bps ?? 0, isZeroLong: false };
    const avgSpeed = entry.samples.reduce((a, b) => a + b, 0) / entry.samples.length;
    const isZeroLong = entry.zeroSince !== null && (Date.now() - entry.zeroSince) > 20_000;
    return { avgSpeed, isZeroLong };
  };

  const [cancelDialog, setCancelDialog] = useState<CancelDialogState>({ open: false, transfer: null, serverName: '' });

  const cancelMutation = useMutation({
    mutationFn: transfersApi.cancel,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['transfers'] });
      setCancelDialog({ open: false, transfer: null, serverName: '' });
    },
  });

  const retryMutation = useMutation({
    mutationFn: transfersApi.retry,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['transfers'] }),
  });

  const pauseMutation = useMutation({
    mutationFn: transfersApi.pause,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['transfers'] }),
  });

  const resumeMutation = useMutation({
    mutationFn: transfersApi.resume,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['transfers'] }),
  });

  const openCancelDialog = useCallback((t: Transfer, serverName: string) => {
    setCancelDialog({ open: true, transfer: t, serverName });
  }, []);

  const serverNameMap = useMemo(() =>
    servers.reduce((acc, s) => ({ ...acc, [s.id]: serverDisplayName(s) }), {} as Record<string, string>),
    [servers]
  );

  const activeStatuses = ['downloading', 'checking', 'queued', 'paused', 'error'];
  const active = transfers.filter(t => activeStatuses.includes(t.status));
  const completed = transfers.filter(t => t.status === 'completed');
  const history = transfers.filter(t => !activeStatuses.includes(t.status) && t.status !== 'completed');
  const failed = transfers.filter(t => t.status === 'failed' || t.status === 'cancelled');

  const stuckCount = active.filter(isStuck).length;
  const healthyCount = active.filter(isHealthy).length;
  const downloadingCount = active.filter(t => t.status === 'downloading' && !isStuck(t)).length;
  const checkingCount = active.filter(t => t.status === 'checking').length;
  const queuedCount = active.filter(t => t.status === 'queued').length;
  const pausedCount = active.filter(t => t.status === 'paused').length;
  const errorCount = active.filter(t => t.status === 'error').length;

  const getServerName = (t: Transfer) =>
    t.destination_server_name || serverNameMap[t.destination_server_id] || 'Unknown';

  // Apply filters
  const applyFilters = (list: Transfer[]) => {
    let filtered = list;

    // Search filter
    if (searchQuery.trim()) {
      const query = searchQuery.toLowerCase();
      filtered = filtered.filter(t =>
        t.package_name?.toLowerCase().includes(query) ||
        getServerName(t).toLowerCase().includes(query)
      );
    }

    // Server filter
    if (serverFilter !== 'all') {
      filtered = filtered.filter(t => t.destination_server_id === serverFilter);
    }

    // Status filter (for active tab)
    if (statusFilter !== 'all' && tab === 'active') {
      filtered = filtered.filter(t => t.status === statusFilter);
    }

    return filtered;
  };

  const filteredActive = applyFilters(active);
  const filteredCompleted = applyFilters(completed);
  const filteredHistory = applyFilters(history);

  // Get unique servers from all transfers for filter
  const allServerIds = [...new Set(transfers.map(t => t.destination_server_id))];

  const currentList = tab === 'active' ? filteredActive : tab === 'completed' ? filteredCompleted : filteredHistory;

  // Get row styling based on status
  const getRowStyle = (t: Transfer, showChecking: boolean, healthy: boolean) => {
    const stuck = isStuck(t);

    if (stuck) return 'bg-yellow-50/50 hover:bg-yellow-50 border-l-4 border-yellow-500';
    if (t.status === 'error') return 'bg-red-50/50 hover:bg-red-50 border-l-4 border-red-500';
    if (t.status === 'paused') return 'bg-orange-50/30 hover:bg-orange-50/50 border-l-4 border-orange-400';
    if (showChecking) return 'bg-amber-50/30 hover:bg-amber-50/50 border-l-4 border-amber-400';
    if (t.status === 'queued') return 'bg-blue-50/20 hover:bg-blue-50/40 border-l-4 border-blue-300';
    if (healthy) return 'bg-gradient-to-r from-green-50/60 to-emerald-50/40 hover:from-green-50 hover:to-emerald-50 border-l-4 border-green-500 animate-pulse-subtle';
    if (t.status === 'downloading') return 'bg-gradient-to-r from-green-50/40 to-blue-50/30 hover:from-green-50/60 hover:to-blue-50/50 border-l-4 border-green-400';
    return 'hover:bg-gray-50/30';
  };

  const getProgressBarStyle = (t: Transfer, showChecking: boolean, healthy: boolean) => {
    const stuck = isStuck(t);

    if (stuck) return 'bg-gradient-to-r from-yellow-400 to-orange-400';
    if (t.status === 'error') return 'bg-gradient-to-r from-red-500 to-red-600';
    if (t.status === 'paused') return 'bg-orange-400';
    if (showChecking) return 'bg-amber-400 animate-pulse';
    if (healthy) return 'bg-gradient-to-r from-green-500 via-emerald-500 to-green-400 animate-gradient';
    if (t.status === 'downloading') return 'bg-gradient-to-r from-green-500 to-blue-500';
    return 'bg-blue-400';
  };

  return (
    <div className="space-y-5">
      {/* Header with Live Stats */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-3xl font-bold text-foreground flex items-center gap-3">
            <Download className="h-7 w-7 text-primary" />
            Transfers
          </h1>
          <div className="flex items-center gap-3 mt-2 text-sm flex-wrap">
            {active.length > 0 && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-blue-50 border border-blue-200">
                <Activity className="h-4 w-4 text-blue-600 animate-pulse" />
                <span className="font-semibold text-blue-700">{active.length} Active</span>
              </div>
            )}
            {healthyCount > 0 && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-green-50 border border-green-200">
                <Zap className="h-4 w-4 text-green-600" />
                <span className="font-semibold text-green-700">{healthyCount} Healthy</span>
              </div>
            )}
            {completed.length > 0 && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-emerald-50 border border-emerald-200">
                <CheckCircle2 className="h-4 w-4 text-emerald-600" />
                <span className="font-semibold text-emerald-700">{completed.length} Completed</span>
              </div>
            )}
            {errorCount > 0 && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-red-50 border border-red-200">
                <XCircle className="h-4 w-4 text-red-600" />
                <span className="font-semibold text-red-700">{errorCount} Error</span>
              </div>
            )}
            {stuckCount > 0 && (
              <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-yellow-50 border border-yellow-200">
                <AlertTriangle className="h-4 w-4 text-yellow-600" />
                <span className="font-semibold text-yellow-700">{stuckCount} Stuck</span>
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Search Bar + Filters */}
      <div className="flex items-center gap-3 flex-wrap">
        <div className="relative flex-1 min-w-[300px]">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <input
            type="text"
            placeholder="Search by package name or server..."
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            className="w-full pl-10 pr-4 py-2.5 rounded-lg border border-border bg-card text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary focus:border-transparent"
          />
        </div>

        {allServerIds.length > 1 && (
          <div className="flex items-center gap-2">
            <Filter className="h-4 w-4 text-muted-foreground" />
            <select
              value={serverFilter}
              onChange={e => setServerFilter(e.target.value)}
              className="rounded-lg border border-border bg-card px-3 py-2.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
            >
              <option value="all">All Servers</option>
              {allServerIds.map(id => (
                <option key={id} value={id}>{serverNameMap[id] || id.slice(0, 8)}</option>
              ))}
            </select>
          </div>
        )}

        {tab === 'active' && (
          <select
            value={statusFilter}
            onChange={e => setStatusFilter(e.target.value)}
            className="rounded-lg border border-border bg-card px-3 py-2.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
          >
            <option value="all">All Statuses</option>
            <option value="downloading">Downloading</option>
            <option value="checking">Checking</option>
            <option value="queued">Queued</option>
            <option value="paused">Paused</option>
            <option value="error">Error</option>
          </select>
        )}
      </div>

      {/* Tabs */}
      <div className="flex gap-2 rounded-xl border border-border bg-card p-1.5">
        {([
          { key: 'active' as Tab, label: 'Active', icon: Activity, count: active.length, color: 'text-blue-600' },
          { key: 'completed' as Tab, label: 'Completed', icon: CheckCircle2, count: completed.length, color: 'text-green-600' },
          { key: 'history' as Tab, label: 'History', icon: History, count: history.length + failed.length, color: 'text-gray-600' },
        ]).map(t => (
          <button key={t.key} onClick={() => { setTab(t.key); setStatusFilter('all'); }}
            className={cn("flex items-center gap-2 rounded-lg px-4 py-2.5 text-sm font-semibold transition-all flex-1 justify-center",
              tab === t.key
                ? "bg-primary text-primary-foreground shadow-lg"
                : "text-muted-foreground hover:text-foreground hover:bg-muted"
            )}>
            <t.icon className={cn("h-4 w-4", tab === t.key ? "" : t.color)} />
            {t.label}
            {t.count > 0 && (
              <span className={cn("ml-1 rounded-full px-2 py-0.5 text-xs font-bold tabular-nums",
                tab === t.key ? "bg-primary-foreground/20 text-primary-foreground" : "bg-muted text-muted-foreground"
              )}>
                {t.count}
              </span>
            )}
          </button>
        ))}
      </div>

      {/* Content */}
      {tab === 'active' && (
        <div className="rounded-xl border border-border bg-card overflow-hidden shadow-sm">
          {isLoading ? (
            <div className="p-8 space-y-3 animate-pulse">
              <div className="h-4 w-1/3 rounded bg-muted" />
              <div className="h-3 w-full rounded bg-muted" />
              <div className="h-3 w-2/3 rounded bg-muted" />
            </div>
          ) : filteredActive.length === 0 ? (
            <div className="p-20 text-center">
              <div className="inline-flex items-center justify-center w-16 h-16 rounded-full bg-primary/10 mb-4">
                <Activity className="h-8 w-8 text-primary" />
              </div>
              <p className="text-lg font-semibold text-foreground">No active transfers</p>
              <p className="text-sm text-muted-foreground mt-2">
                {searchQuery ? 'No transfers match your search' : 'Start a transfer from the DCP Library'}
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-border bg-muted/30">
                    <th className="w-8 px-3" />
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Package</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Server</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider min-w-[240px]">Progress</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Speed</th>
                    <th className="px-4 py-3.5 text-center text-xs font-semibold text-muted-foreground uppercase tracking-wider">Peers</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">ETA</th>
                    <th className="px-4 py-3.5 text-center text-xs font-semibold text-muted-foreground uppercase tracking-wider">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredActive.map(t => {
                    const stuck = isStuck(t);
                    const { avgSpeed, isZeroLong } = getSpeedInfo(t);
                    const healthy = t.status === 'downloading' && avgSpeed > 1024 * 1024 * 10 && t.peers_connected > 0;
                    const showChecking = (t.status === 'checking' || t.status === 'downloading') && isZeroLong;
                    const expanded = expandedId === t.id;
                    return (
                      <>
                        <tr
                          key={t.id}
                          onClick={() => setExpandedId(expanded ? null : t.id)}
                          className={cn("transition-all cursor-pointer group", getRowStyle(t, showChecking, healthy))}
                        >
                          <td className="px-3 text-muted-foreground">
                            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                          </td>
                          <td className="px-4 py-4">
                            <div className="flex items-center gap-2.5">
                              {healthy && <Activity className="h-4 w-4 text-green-500 animate-pulse flex-shrink-0" />}
                              {stuck && <AlertTriangle className="h-4 w-4 text-yellow-500 flex-shrink-0" />}
                              <span className="font-semibold text-foreground truncate block max-w-[250px]" title={t.package_name}>
                                {t.package_name || 'DCP Transfer'}
                              </span>
                            </div>
                          </td>
                          <td className="px-4 py-4 text-muted-foreground text-sm font-medium">
                            {getServerName(t)}
                          </td>
                          <td className="px-4 py-4">
                            <div className="space-y-1.5">
                              <div className="flex items-center gap-3">
                                <div className="h-3 flex-1 rounded-full bg-gray-200 overflow-hidden min-w-[140px] shadow-inner">
                                  <div
                                    className={cn("h-full rounded-full transition-all duration-700", getProgressBarStyle(t, showChecking, healthy))}
                                    style={{ width: `${t.progress_percent}%` }}
                                  />
                                </div>
                                <span className="text-sm tabular-nums font-bold text-foreground w-14 text-right">
                                  {t.progress_percent.toFixed(1)}%
                                </span>
                              </div>
                              {t.status === 'error' && t.error_message && (
                                <p className="text-xs text-red-600 truncate max-w-[280px]" title={t.error_message}>
                                  {t.error_message}
                                </p>
                              )}
                              {t.status === 'downloading' && avgSpeed > 0 && (
                                <div className="flex items-center gap-2 text-xs text-gray-600">
                                  <span className="font-mono">{formatBytes(t.downloaded_bytes)} / {formatBytes(t.total_size_bytes || t.downloaded_bytes)}</span>
                                </div>
                              )}
                            </div>
                          </td>
                          <td className="px-4 py-4 text-right">
                            {t.status === 'paused' ? (
                              <span className="text-orange-600 text-sm font-semibold">paused</span>
                            ) : t.status === 'queued' ? (
                              <span className="text-blue-600 text-sm font-semibold">queued</span>
                            ) : showChecking ? (
                              <span className="text-amber-600 text-sm font-semibold">checking...</span>
                            ) : avgSpeed > 0 ? (
                              <span className={cn("font-mono text-base font-bold tabular-nums",
                                healthy ? "text-green-600" : "text-foreground"
                              )}>
                                {formatSpeed(avgSpeed)}
                              </span>
                            ) : (
                              <span className="text-muted-foreground">—</span>
                            )}
                          </td>
                          <td className="px-4 py-4 text-center">
                            <span className={cn("tabular-nums text-base font-semibold",
                              t.peers_connected > 0 ? "text-foreground" : "text-muted-foreground"
                            )}>
                              {t.peers_connected}
                            </span>
                          </td>
                          <td className="px-4 py-4 text-right">
                            <div className="flex items-center justify-end gap-1.5">
                              <Clock className="h-3.5 w-3.5 text-muted-foreground" />
                              <span className="tabular-nums text-sm font-medium text-muted-foreground">
                                {t.status === 'downloading' && avgSpeed > 0 && t.total_size_bytes > 0
                                  ? formatETA(Math.round((t.total_size_bytes - t.downloaded_bytes) / avgSpeed))
                                  : '—'}
                              </span>
                            </div>
                          </td>
                          <td className="px-4 py-4">
                            <div className="flex items-center justify-center gap-1.5">
                              {(t.status === 'downloading' || t.status === 'checking') && (
                                <button
                                  onClick={e => { e.stopPropagation(); pauseMutation.mutate(t.id); }}
                                  className="rounded-lg p-2 text-orange-600 bg-orange-50 hover:bg-orange-100 border border-orange-200 transition-all hover:scale-105 shadow-sm"
                                  title="Pause transfer"
                                >
                                  <Pause className="h-4 w-4" />
                                </button>
                              )}
                              {t.status === 'paused' && (
                                <button
                                  onClick={e => { e.stopPropagation(); resumeMutation.mutate(t.id); }}
                                  className="rounded-lg p-2 text-green-600 bg-green-50 hover:bg-green-100 border border-green-200 transition-all hover:scale-105 shadow-sm"
                                  title="Resume transfer"
                                >
                                  <Play className="h-4 w-4" />
                                </button>
                              )}
                              {(t.status === 'error' || t.status === 'failed') && (
                                <button
                                  onClick={e => { e.stopPropagation(); retryMutation.mutate(t.id); }}
                                  className="rounded-lg p-2 text-blue-600 bg-blue-50 hover:bg-blue-100 border border-blue-200 transition-all hover:scale-105 shadow-sm"
                                  title="Retry transfer"
                                >
                                  <RotateCcw className="h-4 w-4" />
                                </button>
                              )}
                              <button
                                onClick={e => { e.stopPropagation(); openCancelDialog(t, getServerName(t)); }}
                                className="rounded-lg p-2 text-red-600 bg-red-50 hover:bg-red-100 border border-red-200 transition-all hover:scale-105 shadow-sm"
                                title="Cancel transfer"
                              >
                                <X className="h-4 w-4" />
                              </button>
                            </div>
                          </td>
                        </tr>
                        {expanded && (
                          <tr key={`${t.id}-detail`}>
                            <td colSpan={9} className="px-4 py-3 bg-muted/20">
                              <TransferDetail transfer={t} serverName={getServerName(t)} />
                            </td>
                          </tr>
                        )}
                      </>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* Completed Transfers */}
      {tab === 'completed' && (
        <div className="rounded-xl border border-border bg-card overflow-hidden shadow-sm">
          {filteredCompleted.length === 0 ? (
            <div className="p-20 text-center">
              <div className="inline-flex items-center justify-center w-16 h-16 rounded-full bg-green-50 mb-4">
                <CheckCircle2 className="h-8 w-8 text-green-600" />
              </div>
              <p className="text-lg font-semibold text-foreground">No completed transfers</p>
              <p className="text-sm text-muted-foreground mt-2">
                {searchQuery ? 'No transfers match your search' : 'Completed transfers will appear here'}
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-border bg-muted/30">
                    <th className="w-8 px-3" />
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Package</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Server</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Size</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Duration</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Avg Speed</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Requested By</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Completed</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {filteredCompleted.map(t => {
                    const expanded = expandedId === t.id;
                    const started = t.started_at ? new Date(t.started_at).getTime() : null;
                    const completed = (t.completed_at || t.updated_at) ? new Date(t.completed_at || t.updated_at).getTime() : null;
                    const duration = started && completed ? Math.floor((completed - started) / 1000) : null;
                    const avgSpeed = duration && t.downloaded_bytes ? t.downloaded_bytes / duration : null;

                    return (
                      <>
                        <tr
                          key={t.id}
                          onClick={() => setExpandedId(expanded ? null : t.id)}
                          className="hover:bg-green-50/30 transition-colors group cursor-pointer"
                        >
                          <td className="px-3 text-muted-foreground">
                            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                          </td>
                          <td className="px-4 py-4">
                            <div className="flex items-center gap-2.5">
                              <CheckCircle2 className="h-4 w-4 text-green-600 flex-shrink-0" />
                              <span className="font-semibold text-foreground truncate max-w-xs">{t.package_name || '—'}</span>
                            </div>
                          </td>
                          <td className="px-4 py-4 text-muted-foreground text-sm font-medium">{getServerName(t)}</td>
                          <td className="px-4 py-4 text-right font-mono text-foreground text-sm font-semibold">{formatBytes(t.total_size_bytes || 0)}</td>
                          <td className="px-4 py-4 text-right text-muted-foreground text-sm font-medium">
                            {duration ? formatETA(duration) : '—'}
                          </td>
                          <td className="px-4 py-4 text-right font-mono text-green-600 text-sm font-bold">
                            {avgSpeed ? formatSpeed(avgSpeed) : '—'}
                          </td>
                          <td className="px-4 py-4 text-muted-foreground text-sm">{t.requested_by || '—'}</td>
                          <td className="px-4 py-4 text-right text-muted-foreground whitespace-nowrap text-sm">{formatRelativeTime(t.completed_at || t.updated_at)}</td>
                        </tr>
                        {expanded && (
                          <tr key={`${t.id}-detail`}>
                            <td colSpan={9} className="px-4 py-4 bg-green-50/20">
                              <CompletedTransferDetail transfer={t} serverName={getServerName(t)} />
                            </td>
                          </tr>
                        )}
                      </>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* History Table */}
      {tab === 'history' && (
        <div className="rounded-xl border border-border bg-card overflow-hidden shadow-sm">
          {filteredHistory.length === 0 ? (
            <div className="p-20 text-center">
              <div className="inline-flex items-center justify-center w-16 h-16 rounded-full bg-gray-50 mb-4">
                <History className="h-8 w-8 text-gray-600" />
              </div>
              <p className="text-lg font-semibold text-foreground">No transfer history</p>
              <p className="text-sm text-muted-foreground mt-2">
                {searchQuery ? 'No transfers match your search' : 'Failed and cancelled transfers will appear here'}
              </p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="border-b border-border bg-muted/30">
                    <th className="w-8 px-3" />
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Package</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Server</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Status</th>
                    <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider min-w-[200px]">Error / Info</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Downloaded</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Size</th>
                    <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Date</th>
                    <th className="px-4 py-3.5 text-center text-xs font-semibold text-muted-foreground uppercase tracking-wider">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {filteredHistory.map(t => {
                    const expanded = expandedId === t.id;
                    const isFailed = t.status === 'failed' || t.status === 'error';
                    const isCancelled = t.status === 'cancelled';
                    const hasProgress = t.downloaded_bytes > 0;
                    const progressPct = (t.total_size_bytes && t.total_size_bytes > 0)
                      ? (t.downloaded_bytes / t.total_size_bytes * 100)
                      : 0;
                    return (
                      <>
                        <tr
                          key={t.id}
                          onClick={() => setExpandedId(expanded ? null : t.id)}
                          className={cn(
                            "transition-colors cursor-pointer",
                            isFailed ? "hover:bg-red-50/40 bg-red-50/20" : "hover:bg-gray-50/40"
                          )}
                        >
                          <td className="px-3 text-muted-foreground">
                            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                          </td>
                          <td className="px-4 py-3.5">
                            <span className="font-semibold text-foreground truncate block max-w-[220px]" title={t.package_name}>
                              {t.package_name || '—'}
                            </span>
                            <span className="text-xs text-muted-foreground">→ {getServerName(t)}</span>
                          </td>
                          <td className="px-4 py-3.5 text-muted-foreground text-sm font-medium hidden xl:table-cell">{getServerName(t)}</td>
                          <td className="px-4 py-3.5"><StatusBadge status={t.status} /></td>
                          <td className="px-4 py-3.5 max-w-[220px]">
                            {t.error_message ? (
                              <span className="text-xs text-red-600 truncate block" title={t.error_message}>
                                {t.error_message}
                              </span>
                            ) : isCancelled ? (
                              <span className="text-xs text-muted-foreground">Cancelled by user</span>
                            ) : (
                              <span className="text-xs text-muted-foreground">—</span>
                            )}
                          </td>
                          <td className="px-4 py-3.5 text-right">
                            {hasProgress ? (
                              <div className="flex flex-col items-end gap-1">
                                <span className="font-mono text-sm text-foreground">{formatBytes(t.downloaded_bytes)}</span>
                                {t.total_size_bytes ? (
                                  <div className="w-20 h-1.5 rounded-full bg-gray-200 overflow-hidden">
                                    <div
                                      className={cn("h-full rounded-full", isFailed ? "bg-red-400" : "bg-orange-400")}
                                      style={{ width: `${Math.min(progressPct, 100)}%` }}
                                    />
                                  </div>
                                ) : null}
                              </div>
                            ) : (
                              <span className="text-muted-foreground text-sm">0 B</span>
                            )}
                          </td>
                          <td className="px-4 py-3.5 text-right font-mono text-foreground text-sm">
                            {t.total_size_bytes ? formatBytes(t.total_size_bytes) : '—'}
                          </td>
                          <td className="px-4 py-3.5 text-right text-muted-foreground whitespace-nowrap text-sm">{formatRelativeTime(t.created_at)}</td>
                          <td className="px-4 py-3.5">
                            <div className="flex items-center justify-center gap-1.5">
                              {isFailed && (
                                <button
                                  onClick={e => { e.stopPropagation(); retryMutation.mutate(t.id); }}
                                  disabled={retryMutation.isPending}
                                  className="rounded-lg p-2 text-blue-600 bg-blue-50 hover:bg-blue-100 border border-blue-200 transition-all hover:scale-105 shadow-sm"
                                  title="Retry transfer"
                                >
                                  <RotateCcw className="h-4 w-4" />
                                </button>
                              )}
                            </div>
                          </td>
                        </tr>
                        {expanded && (
                          <tr key={`${t.id}-detail`}>
                            <td colSpan={9} className={cn("px-4 py-4", isFailed ? "bg-red-50/30" : "bg-muted/20")}>
                              <HistoryTransferDetail transfer={t} serverName={getServerName(t)} onRetry={() => retryMutation.mutate(t.id)} />
                            </td>
                          </tr>
                        )}
                      </>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {/* Cancel Confirmation Dialog */}
      {cancelDialog.open && cancelDialog.transfer && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setCancelDialog({ open: false, transfer: null, serverName: '' })}>
          <div className="w-full max-w-md rounded-xl border border-border bg-card shadow-xl mx-4" onClick={e => e.stopPropagation()}>
            <div className="border-b border-border px-5 py-4">
              <h3 className="text-base font-semibold text-foreground">Cancel Transfer</h3>
              <p className="text-sm text-muted-foreground mt-1">
                {cancelDialog.transfer.package_name}
              </p>
            </div>
            <div className="px-5 py-4 space-y-3">
              <p className="text-sm text-foreground">
                Do you want to delete the downloaded DCP data on <strong>{cancelDialog.serverName}</strong>?
              </p>
              {cancelDialog.transfer.progress_percent > 0 && (
                <p className="text-xs text-muted-foreground">
                  {formatBytes(cancelDialog.transfer.downloaded_bytes)} downloaded ({cancelDialog.transfer.progress_percent.toFixed(1)}% complete)
                </p>
              )}
            </div>
            <div className="border-t border-border px-5 py-3 flex justify-end gap-2">
              <button
                onClick={() => setCancelDialog({ open: false, transfer: null, serverName: '' })}
                className="rounded-md px-3 py-1.5 text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                Go Back
              </button>
              <button
                onClick={() => cancelMutation.mutate({ id: cancelDialog.transfer!.id, deleteData: false })}
                disabled={cancelMutation.isPending}
                className="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium bg-muted hover:bg-accent text-foreground transition-colors"
              >
                <FolderOpen className="h-3.5 w-3.5" />
                Keep Data
              </button>
              <button
                onClick={() => cancelMutation.mutate({ id: cancelDialog.transfer!.id, deleteData: true })}
                disabled={cancelMutation.isPending}
                className="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium bg-destructive text-destructive-foreground hover:bg-destructive/90 transition-colors"
              >
                <Trash2 className="h-3.5 w-3.5" />
                Delete DCP
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function TransferDetail({ transfer: t, serverName }: { transfer: Transfer; serverName: string }) {
  const stuck = isStuck(t);
  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-xs">
      <div>
        <p className="text-muted-foreground mb-0.5">Downloaded</p>
        <p className="font-medium text-foreground tabular-nums">
          {formatBytes(t.downloaded_bytes)}
          {t.total_size_bytes ? ` / ${formatBytes(t.total_size_bytes)}` : ''}
        </p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Download Speed</p>
        <p className="font-medium text-foreground tabular-nums">{formatSpeed(t.download_speed_bps)}</p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Upload Speed</p>
        <p className="font-medium text-foreground tabular-nums">{formatSpeed(t.upload_speed_bps)}</p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Peers Connected</p>
        <p className="font-medium text-foreground tabular-nums">{t.peers_connected}</p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Destination</p>
        <p className="font-medium text-foreground">{serverName}</p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Started</p>
        <p className="font-medium text-foreground">
          {t.started_at ? new Date(t.started_at).toLocaleString() : '—'}
        </p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Priority</p>
        <p className="font-medium text-foreground">{t.priority}</p>
      </div>
      <div>
        <p className="text-muted-foreground mb-0.5">Last Updated</p>
        <p className="font-medium text-foreground">{formatRelativeTime(t.updated_at)}</p>
      </div>
      {stuck && (
        <div className="col-span-full">
          <p className="text-yellow-600 flex items-center gap-1 bg-yellow-50 rounded p-2">
            <AlertTriangle className="h-3.5 w-3.5" />
            Transfer appears stuck - 0 speed and 0 peers for more than 5 minutes. The seeder may be offline or unreachable.
          </p>
        </div>
      )}
      {t.error_message && (
        <div className="col-span-full">
          <p className="text-red-600 bg-red-50 rounded p-2">Error: {t.error_message}</p>
        </div>
      )}
    </div>
  );
}

function CompletedTransferDetail({ transfer: t, serverName }: { transfer: Transfer; serverName: string }) {
  const started = t.started_at ? new Date(t.started_at).getTime() : null;
  const completed = (t.completed_at || t.updated_at) ? new Date(t.completed_at || t.updated_at).getTime() : null;
  const duration = started && completed ? Math.floor((completed - started) / 1000) : null;
  const avgSpeed = duration && t.downloaded_bytes ? t.downloaded_bytes / duration : null;

  return (
    <div className="space-y-4">
      {/* Success Banner */}
      <div className="flex items-center gap-2 px-4 py-3 bg-green-50 border border-green-200 rounded-lg">
        <CheckCircle2 className="h-5 w-5 text-green-600 flex-shrink-0" />
        <div className="flex-1">
          <p className="font-semibold text-green-900">Transfer completed successfully</p>
          <p className="text-sm text-green-700">
            {formatBytes(t.downloaded_bytes)} transferred in {duration ? formatETA(duration) : 'unknown time'}
            {avgSpeed && <span className="ml-2">• Average speed: <span className="font-mono font-bold">{formatSpeed(avgSpeed)}</span></span>}
          </p>
        </div>
      </div>

      {/* Detailed Stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-xs">
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Total Size</p>
          <p className="font-mono text-base font-bold text-foreground tabular-nums">{formatBytes(t.downloaded_bytes)}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Duration</p>
          <p className="font-mono text-base font-bold text-foreground tabular-nums">{duration ? formatETA(duration) : '—'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Average Speed</p>
          <p className="font-mono text-base font-bold text-green-600 tabular-nums">{avgSpeed ? formatSpeed(avgSpeed) : '—'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Peak Speed</p>
          <p className="font-mono text-base font-bold text-foreground tabular-nums">{t.download_speed_bps ? formatSpeed(t.download_speed_bps) : '—'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Destination</p>
          <p className="text-sm font-semibold text-foreground">{serverName}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Started</p>
          <p className="text-sm font-medium text-foreground">
            {t.started_at ? new Date(t.started_at).toLocaleString(undefined, {
              month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
            }) : '—'}
          </p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Completed</p>
          <p className="text-sm font-medium text-foreground">
            {completed ? new Date(completed).toLocaleString(undefined, {
              month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
            }) : '—'}
          </p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Requested By</p>
          <p className="text-sm font-medium text-foreground">{t.requested_by || 'Unknown'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Priority</p>
          <p className="text-sm font-medium text-foreground">{t.priority}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Peers Used</p>
          <p className="text-sm font-medium text-foreground tabular-nums">{t.peers_connected || 0}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Upload Contributed</p>
          <p className="font-mono text-sm font-medium text-foreground tabular-nums">{formatSpeed(t.upload_speed_bps)}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Transfer ID</p>
          <p className="text-xs font-mono text-muted-foreground truncate" title={t.id}>{t.id.slice(0, 8)}...</p>
        </div>
      </div>
    </div>
  );
}

function HistoryTransferDetail({ transfer: t, serverName, onRetry }: { transfer: Transfer; serverName: string; onRetry: () => void }) {
  const isFailed = t.status === 'failed' || t.status === 'error';
  const isCancelled = t.status === 'cancelled';
  const hasProgress = t.downloaded_bytes > 0;
  const progressPct = (t.total_size_bytes && t.total_size_bytes > 0)
    ? (t.downloaded_bytes / t.total_size_bytes * 100)
    : 0;
  const started = t.started_at ? new Date(t.started_at).getTime() : null;
  const ended = (t.updated_at) ? new Date(t.updated_at).getTime() : null;
  const duration = started && ended ? Math.floor((ended - started) / 1000) : null;

  return (
    <div className="space-y-4">
      {/* Status Banner */}
      {isFailed && (
        <div className="flex items-start gap-3 px-4 py-3 bg-red-50 border border-red-200 rounded-lg">
          <XCircle className="h-5 w-5 text-red-600 flex-shrink-0 mt-0.5" />
          <div className="flex-1 min-w-0">
            <p className="font-semibold text-red-900">Transfer failed</p>
            {t.error_message ? (
              <p className="text-sm text-red-700 mt-0.5 break-words">{t.error_message}</p>
            ) : (
              <p className="text-sm text-red-600 mt-0.5">No error message recorded. The transfer may have failed due to connectivity issues or the source going offline.</p>
            )}
          </div>
          <button
            onClick={onRetry}
            className="flex-shrink-0 inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium bg-blue-600 text-white hover:bg-blue-700 transition-colors shadow-sm"
          >
            <RotateCcw className="h-3.5 w-3.5" />
            Retry
          </button>
        </div>
      )}
      {isCancelled && (
        <div className="flex items-center gap-3 px-4 py-3 bg-gray-50 border border-gray-200 rounded-lg">
          <XCircle className="h-5 w-5 text-gray-500 flex-shrink-0" />
          <p className="text-sm text-gray-700">Transfer was cancelled{hasProgress ? ` after downloading ${formatBytes(t.downloaded_bytes)}` : ''}</p>
        </div>
      )}

      {/* Progress Info (if any data was downloaded) */}
      {hasProgress && t.total_size_bytes && t.total_size_bytes > 0 && (
        <div className="space-y-2">
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>Progress before {isFailed ? 'failure' : 'cancellation'}</span>
            <span className="font-mono">{formatBytes(t.downloaded_bytes)} / {formatBytes(t.total_size_bytes)} ({progressPct.toFixed(1)}%)</span>
          </div>
          <div className="h-3 w-full rounded-full bg-gray-200 overflow-hidden shadow-inner">
            <div
              className={cn("h-full rounded-full", isFailed ? "bg-gradient-to-r from-red-400 to-red-500" : "bg-gradient-to-r from-orange-400 to-orange-500")}
              style={{ width: `${Math.min(progressPct, 100)}%` }}
            />
          </div>
        </div>
      )}

      {/* Detail Grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 text-xs">
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Package</p>
          <p className="text-sm font-semibold text-foreground truncate" title={t.package_name}>{t.package_name || '—'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Destination</p>
          <p className="text-sm font-semibold text-foreground">{serverName}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Total Size</p>
          <p className="font-mono text-sm font-bold text-foreground tabular-nums">
            {t.total_size_bytes ? formatBytes(t.total_size_bytes) : 'Unknown'}
          </p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Downloaded</p>
          <p className="font-mono text-sm font-bold text-foreground tabular-nums">{formatBytes(t.downloaded_bytes)}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Requested By</p>
          <p className="text-sm font-medium text-foreground">{t.requested_by || '—'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Started</p>
          <p className="text-sm font-medium text-foreground">
            {t.started_at ? new Date(t.started_at).toLocaleString(undefined, {
              month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
            }) : 'Not started'}
          </p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Failed / Ended</p>
          <p className="text-sm font-medium text-foreground">
            {t.updated_at ? new Date(t.updated_at).toLocaleString(undefined, {
              month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
            }) : '—'}
          </p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Duration</p>
          <p className="text-sm font-medium text-foreground">{duration && duration > 0 ? formatETA(duration) : '—'}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Priority</p>
          <p className="text-sm font-medium text-foreground">{t.priority}</p>
        </div>
        <div>
          <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Transfer ID</p>
          <p className="text-xs font-mono text-muted-foreground truncate" title={t.id}>{t.id.slice(0, 8)}...</p>
        </div>
        {t.torrent_id && (
          <div>
            <p className="text-muted-foreground mb-1 uppercase tracking-wide font-medium">Torrent ID</p>
            <p className="text-xs font-mono text-muted-foreground truncate" title={t.torrent_id}>{t.torrent_id.slice(0, 8)}...</p>
          </div>
        )}
      </div>
    </div>
  );
}
