import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import { useDCPs } from '@/hooks/useDCPs';
import { useTorrents } from '@/hooks/useTorrents';
import { useServers } from '@/hooks/useServers';
import { useTransfers } from '@/hooks/useTransfers';
import { useDebounce } from '@/hooks/useDebounce';
import { dcpsApi } from '@/api/dcps';
import { transfersApi } from '@/api/transfers';
import { serverDisplayName } from '@/api/servers';
import { formatBytes, formatSpeed, formatETA, formatRelativeTime } from '@/utils/format';
import {
  Search, Film, Clapperboard, Megaphone, FileVideo, Package,
  Download, CheckCircle2, Clock, X, Server as ServerIcon, ChevronRight,
  Filter, Radio, Trash2, Pause, Play, RotateCcw, AlertTriangle, Loader2,
  Link2, FolderDown, ArrowDownToLine, Building2, Check,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import type { DCPPackage, Torrent, PackageServerStatus } from '@/api/types';

const kindIcons: Record<string, typeof Film> = {
  feature: Clapperboard,
  trailer: FileVideo,
  advertisement: Megaphone,
};

const kindFilters = [
  { value: '', label: 'All' },
  { value: 'feature', label: 'Features' },
  { value: 'trailer', label: 'Trailers' },
  { value: 'advertisement', label: 'Ads' },
  { value: 'other', label: 'Other' },
];

interface DeleteDialogState {
  open: boolean;
  serverStatuses: PackageServerStatus[];
  adminPassword: string;
  hasRosettaBridge: boolean;
}

export default function DCPLibrary() {
  const [search, setSearch] = useState('');
  const [kindFilter, setKindFilter] = useState('');
  // siteFilterIds: which servers to filter by (empty = show all)
  const [siteFilterIds, setSiteFilterIds] = useState<Set<string>>(new Set());
  const [sitePopoverOpen, setSitePopoverOpen] = useState(false);
  const sitePopoverRef = useRef<HTMLDivElement>(null);
  const [selectedDCP, setSelectedDCP] = useState<DCPPackage | null>(null);
  const [selectedSites, setSelectedSites] = useState<Set<string>>(new Set());
  const [deleteDialog, setDeleteDialog] = useState<DeleteDialogState>({ open: false, serverStatuses: [], adminPassword: '', hasRosettaBridge: false });

  // Debounce search — only re-filter after user stops typing
  const debouncedSearch = useDebounce(search, 300);

  // When site filter is active, pass server_ids to backend so it returns
  // only DCPs that exist on those servers. Otherwise load everything.
  const { data: dcpPage, isLoading } = useDCPs(
    siteFilterIds.size > 0 ? { server_ids: Array.from(siteFilterIds) } : undefined
  );
  const allDcps: DCPPackage[] = dcpPage?.dcps ?? [];

  // Client-side filter: search + kind on whatever the backend returned
  const filtered = useMemo(() => {
    let items = allDcps;
    if (debouncedSearch) {
      const q = debouncedSearch.toLowerCase();
      items = items.filter(d =>
        d.package_name.toLowerCase().includes(q) ||
        (d.content_title || '').toLowerCase().includes(q)
      );
    }
    if (kindFilter) {
      items = items.filter(d => d.content_kind.toLowerCase() === kindFilter);
    }
    return items;
  }, [allDcps, debouncedSearch, kindFilter]);

  // Close site popover on outside click
  useEffect(() => {
    if (!sitePopoverOpen) return;
    const handler = (e: MouseEvent) => {
      if (sitePopoverRef.current && !sitePopoverRef.current.contains(e.target as Node)) {
        setSitePopoverOpen(false);
      }
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [sitePopoverOpen]);

  const { data: torrentsRaw } = useTorrents();
  const { data: serversRaw } = useServers();
  const torrents = Array.isArray(torrentsRaw) ? torrentsRaw : [];
  const servers = Array.isArray(serversRaw) ? serversRaw : [];

  const { data: transfersRaw } = useTransfers();
  const transfers = Array.isArray(transfersRaw) ? transfersRaw : [];

  // Create torrent lookup map
  const torrentMap = useMemo(() => {
    const map = new Map<string, Torrent>();
    torrents.forEach(t => {
      if (t.package_id) map.set(t.package_id, t);
    });
    return map;
  }, [torrents]);

  // Track which packages have active downloads (for left panel indicator)
  const activeDownloadPackages = useMemo(() => {
    const set = new Set<string>();
    transfers.forEach(t => {
      if (t.package_id && (t.status === 'downloading' || t.status === 'checking' || t.status === 'queued')) {
        set.add(t.package_id);
      }
    });
    return set;
  }, [transfers]);

  // Virtual scrolling — only ~20 DOM nodes regardless of filtered list size
  const listRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: filtered.length,
    getScrollElement: () => listRef.current,
    estimateSize: () => 60,
    overscan: 10,
  });

  // Get torrent for selected DCP
  const selectedTorrent = selectedDCP ? torrentMap.get(selectedDCP.id) : null;

  // Per-server status for selected DCP (polls every 5s)
  const { data: serverStatuses = [] } = useQuery({
    queryKey: ['package-server-status', selectedDCP?.id],
    queryFn: () => dcpsApi.getPackageServerStatus(selectedDCP!.id),
    enabled: !!selectedDCP,
    refetchInterval: 5000,
  });

  // Compute distribution stats
  const distStats = useMemo(() => {
    const total = servers.length;
    let complete = 0, downloading = 0, error = 0, missing = 0;
    const statusMap = new Map<string, PackageServerStatus>();
    serverStatuses.forEach(s => statusMap.set(s.server_id, s));

    servers.forEach(srv => {
      const st = statusMap.get(srv.id);
      if (!st || st.status === 'missing') { missing++; return; }
      if (st.status === 'seeding' || st.status === 'complete') { complete++; return; }
      if (st.status === 'downloading' || st.status === 'paused' || st.status === 'checking' || st.status === 'queued') { downloading++; return; }
      if (st.status === 'error' || st.status === 'incomplete') { error++; return; }
      missing++;
    });
    return { total, complete, downloading, error, missing };
  }, [servers, serverStatuses]);

  // Build ordered server list: active transfers first, then complete/seeding, then missing
  const orderedServers = useMemo(() => {
    const statusMap = new Map<string, PackageServerStatus>();
    serverStatuses.forEach(s => statusMap.set(s.server_id, s));

    const statusOrder: Record<string, number> = {
      downloading: 0, checking: 1, paused: 2, queued: 3,
      error: 4, incomplete: 5, seeding: 6, complete: 7, missing: 8,
    };

    return [...servers].sort((a, b) => {
      const sa = statusMap.get(a.id)?.status || 'missing';
      const sb = statusMap.get(b.id)?.status || 'missing';
      return (statusOrder[sa] ?? 9) - (statusOrder[sb] ?? 9);
    });
  }, [servers, serverStatuses]);

  // Status lookup
  const statusMap = useMemo(() => {
    const map = new Map<string, PackageServerStatus>();
    serverStatuses.forEach(s => map.set(s.server_id, s));
    return map;
  }, [serverStatuses]);

  // Sites that are missing content
  const sitesMissingContent = useMemo(() => {
    return servers.filter(s => {
      const st = statusMap.get(s.id);
      return !st || st.status === 'missing';
    });
  }, [servers, statusMap]);

  // Sites with content (for deletion)
  const sitesWithContent = useMemo(() => {
    return serverStatuses.filter(ss =>
      ss.status === 'seeding' || ss.status === 'complete' ||
      ss.status === 'error' || ss.status === 'incomplete'
    );
  }, [serverStatuses]);

  const toggleSiteSelection = (siteId: string) => {
    const newSet = new Set(selectedSites);
    if (newSet.has(siteId)) newSet.delete(siteId);
    else newSet.add(siteId);
    setSelectedSites(newSet);
  };

  const selectAllMissing = () => {
    setSelectedSites(new Set(sitesMissingContent.map(s => s.id)));
  };

  const selectAllWithContent = () => {
    setSelectedSites(new Set(sitesWithContent.map(s => s.server_id)));
  };

  const clearSelection = () => setSelectedSites(new Set());

  const queryClient = useQueryClient();
  const invalidateAll = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: ['package-server-status'] });
    queryClient.invalidateQueries({ queryKey: ['transfers'] });
  }, [queryClient]);

  // Start transfer mutation
  const startTransferMutation = useMutation({
    mutationFn: async () => {
      if (!selectedTorrent) return;
      const sites = Array.from(selectedSites);
      for (const siteId of sites) {
        await transfersApi.create({
          torrent_id: selectedTorrent.id,
          destination_server_id: siteId,
          requested_by: 'dashboard',
        });
      }
    },
    onSuccess: () => {
      invalidateAll();
      setSelectedSites(new Set());
    },
  });

  // Track per-server delete results for live feedback
  const [deleteResults, setDeleteResults] = useState<Record<string, { status: 'pending' | 'success' | 'error'; message: string }>>({});

  // Bulk delete mutation - uses WebSocket for real-time deletion
  const bulkDeleteMutation = useMutation({
    mutationFn: async (serverStatuses: PackageServerStatus[]) => {
      if (!selectedDCP) return;
      const results: Record<string, { status: 'pending' | 'success' | 'error'; message: string }> = {};
      for (const ss of serverStatuses) {
        results[ss.server_id] = { status: 'pending', message: 'Sending delete command...' };
      }
      setDeleteResults({ ...results });

      for (const ss of serverStatuses) {
        try {
          const resp = await dcpsApi.deleteContentWS(ss.server_id, {
            package_id: selectedDCP.id,
          });
          results[ss.server_id] = {
            status: resp.success ? 'success' : 'error',
            message: resp.message || resp.error || (resp.success ? 'Deleted' : 'Failed'),
          };
        } catch (err: unknown) {
          const errMsg = err instanceof Error ? err.message : 'Connection failed';
          results[ss.server_id] = { status: 'error', message: errMsg };
        }
        setDeleteResults({ ...results });
      }
    },
    onSuccess: () => {
      invalidateAll();
      // Auto-close dialog after a brief delay to show results
      setTimeout(() => {
        setDeleteDialog({ open: false, serverStatuses: [], adminPassword: '', hasRosettaBridge: false });
        setDeleteResults({});
        setSelectedSites(new Set());
      }, 1500);
    },
  });

  // Pause/Resume/Cancel via transfer API
  const pauseMutation = useMutation({
    mutationFn: transfersApi.pause,
    onSuccess: invalidateAll,
  });
  const resumeMutation = useMutation({
    mutationFn: transfersApi.resume,
    onSuccess: invalidateAll,
  });
  const cancelMutation = useMutation({
    mutationFn: (args: { id: string; deleteData: boolean }) => transfersApi.cancel(args),
    onSuccess: invalidateAll,
  });

  // Retry = create new transfer
  const retryMutation = useMutation({
    mutationFn: async (serverId: string) => {
      if (!selectedTorrent) return;
      await transfersApi.create({
        torrent_id: selectedTorrent.id,
        destination_server_id: serverId,
        requested_by: 'dashboard',
      });
    },
    onSuccess: invalidateAll,
  });

  const handleStartTransfer = () => {
    if (selectedSites.size === 0 || !selectedTorrent) return;
    startTransferMutation.mutate();
  };

  const handleBulkDelete = () => {
    const selected = sitesWithContent.filter(ss => selectedSites.has(ss.server_id));
    if (selected.length === 0) return;
    const hasRB = selected.some(ss => ss.rosettabridge_path);
    setDeleteDialog({ open: true, serverStatuses: selected, adminPassword: '', hasRosettaBridge: hasRB });
  };

  return (
    <div className="flex h-[calc(100vh-64px)] bg-white">
      {/* Left Panel - Content List (full width when nothing selected) */}
      <div className={cn(
        "flex-1 flex flex-col min-w-0",
        selectedDCP && "border-r border-gray-200"
      )}>
        {/* Toolbar */}
        <div className="flex items-center gap-3 p-4 border-b border-gray-200 bg-gray-50 flex-wrap">
          {/* Search */}
          <div className="relative flex-1 min-w-[180px] max-w-md">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
            <input
              value={search}
              onChange={e => setSearch(e.target.value)}
              placeholder="Search content..."
              className="h-9 w-full rounded border border-gray-300 bg-white pl-10 pr-3 text-sm text-gray-900 placeholder:text-gray-500 focus:outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500"
            />
          </div>

          {/* Kind filters */}
          <div className="flex items-center gap-2">
            {kindFilters.map(filter => (
              <button
                key={filter.value}
                onClick={() => setKindFilter(filter.value)}
                className={cn(
                  "px-3 py-1.5 text-xs font-medium rounded transition-colors",
                  kindFilter === filter.value
                    ? "bg-blue-600 text-white"
                    : "bg-white text-gray-600 border border-gray-300 hover:border-gray-400"
                )}
              >
                {filter.label}
              </button>
            ))}
          </div>

          {/* Site filter */}
          <div className="relative" ref={sitePopoverRef}>
            <button
              onClick={() => setSitePopoverOpen(v => !v)}
              className={cn(
                "flex items-center gap-1.5 h-9 px-3 text-xs font-medium rounded border transition-colors",
                siteFilterIds.size > 0
                  ? "bg-blue-600 text-white border-blue-600"
                  : "bg-white text-gray-600 border-gray-300 hover:border-gray-400"
              )}
            >
              <Building2 className="h-3.5 w-3.5" />
              Sites
              {siteFilterIds.size > 0 && (
                <span className="ml-0.5 bg-white text-blue-700 rounded-full px-1.5 py-0 text-[10px] font-bold leading-4">
                  {siteFilterIds.size}
                </span>
              )}
            </button>

            {sitePopoverOpen && (
              <div className="absolute top-full left-0 mt-1 z-50 w-64 rounded-lg border border-gray-200 bg-white shadow-lg">
                <div className="flex items-center justify-between px-3 py-2 border-b border-gray-100">
                  <span className="text-xs font-semibold text-gray-700">Filter by site</span>
                  {siteFilterIds.size > 0 && (
                    <button
                      onClick={() => setSiteFilterIds(new Set())}
                      className="text-[10px] text-blue-600 hover:text-blue-700 font-medium"
                    >
                      Clear all
                    </button>
                  )}
                </div>
                <div className="max-h-64 overflow-y-auto py-1">
                  {servers.length === 0 ? (
                    <p className="px-3 py-4 text-xs text-gray-400 text-center">No sites registered</p>
                  ) : (
                    servers.map(server => {
                      const isChecked = siteFilterIds.has(server.id);
                      return (
                        <button
                          key={server.id}
                          onClick={() => {
                            setSiteFilterIds(prev => {
                              const next = new Set(prev);
                              if (next.has(server.id)) next.delete(server.id);
                              else next.add(server.id);
                              return next;
                            });
                          }}
                          className="w-full flex items-center gap-2.5 px-3 py-2 text-left hover:bg-gray-50 transition-colors"
                        >
                          <div className={cn(
                            "w-4 h-4 rounded border flex items-center justify-center shrink-0 transition-colors",
                            isChecked ? "bg-blue-600 border-blue-600" : "border-gray-300"
                          )}>
                            {isChecked && <Check className="h-2.5 w-2.5 text-white" />}
                          </div>
                          <div className="flex items-center gap-1.5 min-w-0">
                            <div className={cn(
                              "w-1.5 h-1.5 rounded-full shrink-0",
                              server.is_authorized ? "bg-green-500" : "bg-gray-400"
                            )} />
                            <span className="text-xs text-gray-800 truncate">{serverDisplayName(server)}</span>
                          </div>
                        </button>
                      );
                    })
                  )}
                </div>
                {siteFilterIds.size > 0 && (
                  <div className="border-t border-gray-100 px-3 py-2">
                    <p className="text-[10px] text-gray-400">
                      Showing DCPs present on {siteFilterIds.size === 1 ? 'this site' : `any of these ${siteFilterIds.size} sites`}
                    </p>
                  </div>
                )}
              </div>
            )}
          </div>

          {/* Result count */}
          <div className="flex items-center gap-2 text-xs text-gray-600 px-2 border-l border-gray-300">
            <Filter className="h-4 w-4" />
            <span>{filtered.length} / {allDcps.length}</span>
          </div>
        </div>

        {/* Content List - virtualised */}
        <div ref={listRef} className="flex-1 overflow-y-auto">
          {isLoading ? (
            <div className="p-4 space-y-2">
              {Array.from({ length: 10 }).map((_, i) => (
                <div key={i} className="h-12 rounded bg-gray-200 animate-pulse" />
              ))}
            </div>
          ) : filtered.length === 0 ? (
            <div className="flex flex-col items-center justify-center h-full text-gray-500">
              <Package className="h-12 w-12 mb-3 opacity-30" />
              <p className="text-sm font-medium">No content found</p>
              <p className="text-xs mt-1">Try adjusting your search or filters</p>
            </div>
          ) : (
            <div style={{ height: `${virtualizer.getTotalSize()}px`, position: 'relative' }}>
              {virtualizer.getVirtualItems().map(vItem => {
                const dcp = filtered[vItem.index];
                const KindIcon = kindIcons[dcp.content_kind.toLowerCase()] || Film;
                const hasTorrent = torrentMap.has(dcp.id);
                const isSelected = selectedDCP?.id === dcp.id;
                const isDownloading = activeDownloadPackages.has(dcp.id);

                return (
                  <div
                    key={dcp.id}
                    data-index={vItem.index}
                    ref={virtualizer.measureElement}
                    style={{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${vItem.start}px)` }}
                    onClick={() => setSelectedDCP(dcp)}
                    className={cn(
                      "flex items-center gap-3 px-4 py-3 cursor-pointer transition-colors border-l-2 border-b border-gray-200",
                      isSelected
                        ? "bg-blue-50 border-l-blue-600"
                        : isDownloading
                          ? "bg-blue-50/40 border-l-blue-300 hover:bg-blue-50/60"
                          : "hover:bg-gray-50 border-l-transparent"
                    )}
                  >
                    <div className="relative">
                      <div className={cn(
                        "flex items-center justify-center w-8 h-8 rounded",
                        isDownloading ? "bg-blue-100" : hasTorrent ? "bg-green-100" : "bg-gray-200"
                      )}>
                        <KindIcon className={cn(
                          "h-4 w-4",
                          isDownloading ? "text-blue-600" : hasTorrent ? "text-green-600" : "text-gray-600"
                        )} />
                      </div>
                      {isDownloading && (
                        <div className="absolute -top-1 -right-1 w-3.5 h-3.5 bg-blue-500 rounded-full flex items-center justify-center">
                          <ArrowDownToLine className="h-2 w-2 text-white animate-pulse" />
                        </div>
                      )}
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-gray-900 truncate">
                          {dcp.content_title || dcp.package_name}
                        </span>
                        {hasTorrent && !isDownloading && (
                          <CheckCircle2 className="h-4 w-4 text-green-600 shrink-0" />
                        )}
                        {isDownloading && (
                          <span className="text-[9px] font-semibold uppercase tracking-wider px-1.5 py-0.5 rounded bg-blue-100 text-blue-700 shrink-0">
                            Downloading
                          </span>
                        )}
                      </div>
                      {dcp.content_title && dcp.package_name && dcp.content_title !== dcp.package_name && (
                        <p className="text-xs text-gray-500 truncate">{dcp.package_name}</p>
                      )}
                    </div>
                    <div className="flex items-center gap-4 text-xs text-gray-500">
                      <span className="capitalize">{dcp.content_kind}</span>
                      <span className="hidden md:inline">{dcp.issuer || '\u2014'}</span>
                      <span className="font-mono text-gray-600">{formatBytes(dcp.total_size_bytes)}</span>
                      <span className="hidden sm:inline">{dcp.file_count}</span>
                      <span className="text-gray-400">{formatRelativeTime(dcp.discovered_at)}</span>
                    </div>
                    <ChevronRight className={cn(
                      "h-4 w-4 transition-colors",
                      isSelected ? "text-blue-600" : "text-gray-400"
                    )} />
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {/* Right Panel - Details & Content Management (only when a DCP is selected) */}
      {selectedDCP && (
      <div className="w-[420px] flex flex-col bg-white border-l border-gray-200 shrink-0">
        <>
            {/* Header */}
            <div className="px-4 py-3 border-b border-gray-200">
              <div className="flex items-center justify-between mb-1">
                <h3 className="text-sm font-semibold text-gray-900">Content Manager</h3>
                <button
                  onClick={() => { setSelectedDCP(null); setSelectedSites(new Set()); }}
                  className="p-1 text-gray-400 hover:text-gray-600 hover:bg-gray-100 rounded transition-colors"
                >
                  <X className="h-4 w-4" />
                </button>
              </div>
              <p className="text-xs text-gray-600 truncate font-medium">{selectedDCP.content_title || selectedDCP.package_name}</p>
              <p className="text-[11px] text-gray-400 mt-0.5">
                {formatBytes(selectedDCP.total_size_bytes)} &middot; {selectedDCP.file_count} files
              </p>
            </div>

            {/* Distribution Summary Bar */}
            <div className="px-4 py-2.5 border-b border-gray-200 bg-gray-50">
              <div className="flex h-2 rounded-full overflow-hidden bg-gray-200 mb-2">
                {distStats.complete > 0 && (
                  <div className="bg-green-500" style={{ width: `${(distStats.complete / distStats.total) * 100}%` }} />
                )}
                {distStats.downloading > 0 && (
                  <div className="bg-blue-500" style={{ width: `${(distStats.downloading / distStats.total) * 100}%` }} />
                )}
                {distStats.error > 0 && (
                  <div className="bg-red-400" style={{ width: `${(distStats.error / distStats.total) * 100}%` }} />
                )}
              </div>
              <div className="flex items-center justify-between text-[10px] text-gray-600">
                <div className="flex items-center gap-3">
                  {distStats.complete > 0 && (
                    <span className="flex items-center gap-1">
                      <div className="w-2 h-2 rounded-full bg-green-500" />
                      {distStats.complete} Complete
                    </span>
                  )}
                  {distStats.downloading > 0 && (
                    <span className="flex items-center gap-1">
                      <div className="w-2 h-2 rounded-full bg-blue-500" />
                      {distStats.downloading} Active
                    </span>
                  )}
                  {distStats.error > 0 && (
                    <span className="flex items-center gap-1">
                      <div className="w-2 h-2 rounded-full bg-red-400" />
                      {distStats.error} Error
                    </span>
                  )}
                </div>
                <span className="text-gray-500">{distStats.missing} Missing</span>
              </div>
            </div>

            {/* Action Bar */}
            <div className="px-4 py-2.5 border-b border-gray-200 flex items-center gap-2">
              {selectedSites.size === 0 ? (
                <>
                  <button
                    onClick={selectAllMissing}
                    disabled={sitesMissingContent.length === 0}
                    className={cn(
                      "flex-1 h-8 text-xs font-medium rounded transition-colors",
                      sitesMissingContent.length > 0
                        ? "bg-blue-600 hover:bg-blue-700 text-white"
                        : "bg-gray-100 text-gray-400 cursor-not-allowed"
                    )}
                  >
                    Select Missing ({sitesMissingContent.length})
                  </button>
                  <button
                    onClick={selectAllWithContent}
                    disabled={sitesWithContent.length === 0}
                    className={cn(
                      "flex-1 h-8 text-xs font-medium rounded transition-colors",
                      sitesWithContent.length > 0
                        ? "bg-gray-600 hover:bg-gray-700 text-white"
                        : "bg-gray-100 text-gray-400 cursor-not-allowed"
                    )}
                  >
                    Select All ({sitesWithContent.length})
                  </button>
                </>
              ) : (
                <>
                  <button
                    onClick={clearSelection}
                    className="h-8 px-3 bg-gray-200 hover:bg-gray-300 text-gray-700 text-xs font-medium rounded transition-colors"
                  >
                    Clear ({selectedSites.size})
                  </button>
                  {selectedSites.size > 0 && sitesMissingContent.some(s => selectedSites.has(s.id)) && (
                    <button
                      onClick={handleStartTransfer}
                      disabled={!selectedTorrent || startTransferMutation.isPending}
                      className={cn(
                        "flex-1 h-8 text-xs font-medium rounded transition-colors flex items-center justify-center gap-1.5",
                        selectedTorrent && !startTransferMutation.isPending
                          ? "bg-green-600 hover:bg-green-700 text-white"
                          : "bg-gray-200 text-gray-500 cursor-not-allowed"
                      )}
                    >
                      <Download className="h-3.5 w-3.5" />
                      {startTransferMutation.isPending ? 'Starting...' : 'Transfer'}
                    </button>
                  )}
                  {selectedSites.size > 0 && sitesWithContent.some(s => selectedSites.has(s.server_id)) && (
                    <button
                      onClick={handleBulkDelete}
                      disabled={bulkDeleteMutation.isPending}
                      className="flex-1 h-8 text-xs font-medium rounded transition-colors flex items-center justify-center gap-1.5 bg-red-600 hover:bg-red-700 text-white"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      Delete
                    </button>
                  )}
                </>
              )}
            </div>

            {/* Feedback Messages */}
            {startTransferMutation.isError && (
              <div className="px-4 py-2 bg-red-50 border-b border-red-100">
                <p className="text-[11px] text-red-600">
                  Failed: {(startTransferMutation.error as Error).message}
                </p>
              </div>
            )}
            {!selectedTorrent && selectedSites.size > 0 && sitesMissingContent.some(s => selectedSites.has(s.id)) && (
              <div className="px-4 py-2 bg-amber-50 border-b border-amber-100">
                <p className="text-[11px] text-amber-700 flex items-center gap-1">
                  <Clock className="h-3 w-3" />
                  Torrent not yet generated
                </p>
              </div>
            )}

            {/* Server List */}
            <div className="flex-1 overflow-y-auto">
              <div className="p-3">
                {servers.length === 0 ? (
                  <p className="text-xs text-gray-500 py-8 text-center">No sites registered</p>
                ) : (
                  <div className="space-y-1.5">
                    {orderedServers.map(server => {
                      const ss = statusMap.get(server.id);
                      const status = ss?.status || 'missing';
                      const isMissing = status === 'missing';
                      const isSelected = selectedSites.has(server.id);
                      const isSeeding = status === 'seeding';
                      const isComplete = status === 'complete';
                      const isActive = status === 'downloading' || status === 'checking';
                      const isPaused = status === 'paused';
                      const isError = status === 'error' || status === 'incomplete';
                      const hasTransfer = !!ss?.transfer_id;

                      return (
                        <div
                          key={server.id}
                          onClick={() => toggleSiteSelection(server.id)}
                          className={cn(
                            "rounded border p-2.5 transition-all cursor-pointer",
                            isSelected && "ring-2 ring-blue-400 ring-offset-1",
                            isSeeding && !isSelected && "border-green-300 bg-green-50/30",
                            isComplete && !isSelected && "border-green-200 bg-green-50/20",
                            isActive && !isSelected && "border-blue-300 bg-blue-50/30",
                            isPaused && !isSelected && "border-orange-200 bg-orange-50/20",
                            isError && !isSelected && "border-red-200 bg-red-50/20",
                            isMissing && !isSelected && "border-gray-200 bg-white hover:border-gray-300"
                          )}
                        >
                          {/* Server name row */}
                          <div className="flex items-center gap-2 mb-1">
                            {/* Status indicator */}
                            <div className={cn(
                              "w-3 h-3 rounded-full shrink-0 flex items-center justify-center",
                              isSelected && "bg-blue-600",
                              !isSelected && isSeeding && "bg-green-500",
                              !isSelected && isComplete && "bg-green-400",
                              !isSelected && isActive && "bg-blue-500",
                              !isSelected && isPaused && "bg-orange-400",
                              !isSelected && isError && "bg-red-400",
                              !isSelected && isMissing && "bg-gray-300"
                            )}>
                              {isSelected && <CheckCircle2 className="h-2.5 w-2.5 text-white" />}
                              {!isSelected && isActive && <Loader2 className="h-2 w-2 text-white animate-spin" />}
                            </div>

                            <ServerIcon className="h-3 w-3 text-gray-400 shrink-0" />
                            <span className="text-xs font-medium text-gray-900 truncate flex-1">
                              {serverDisplayName(server)}
                            </span>

                            {/* Compact status badge (only for non-seeding/complete) */}
                            {!isSeeding && !isComplete && !isMissing && (
                              <span className={cn(
                                "text-[9px] font-semibold uppercase tracking-wider px-1.5 py-0.5 rounded shrink-0",
                                isActive && "bg-blue-100 text-blue-700",
                                isPaused && "bg-orange-100 text-orange-700",
                                isError && "bg-red-100 text-red-700"
                              )}>
                                {isPaused ? 'Paused' : isError ? 'Error' : status}
                              </span>
                            )}
                          </div>

                          {/* Progress bar for active/paused/incomplete */}
                          {ss && !isMissing && ss.progress_percent > 0 && ss.progress_percent < 100 && (
                            <div className="mb-1.5">
                              <div className="flex items-center gap-2">
                                <div className="h-1 flex-1 rounded-full bg-gray-200 overflow-hidden">
                                  <div
                                    className={cn(
                                      "h-full rounded-full transition-all duration-500",
                                      isActive && "bg-blue-500",
                                      isPaused && "bg-orange-400",
                                      isError && "bg-red-400"
                                    )}
                                    style={{ width: `${ss.progress_percent}%` }}
                                  />
                                </div>
                                <span className="text-[10px] tabular-nums font-medium text-gray-600 w-9 text-right">
                                  {ss.progress_percent.toFixed(1)}%
                                </span>
                              </div>
                            </div>
                          )}

                          {/* Transfer details for active downloads */}
                          {isActive && ss && (
                            <div className="flex items-center gap-2 text-[9px] text-gray-500 mb-1">
                              {ss.download_speed_bps > 0 && (
                                <span className="font-mono font-medium text-blue-600">
                                  {formatSpeed(ss.download_speed_bps)}
                                </span>
                              )}
                              {ss.eta_seconds && ss.eta_seconds > 0 && (
                                <span>ETA {formatETA(ss.eta_seconds)}</span>
                              )}
                              {ss.peers_connected > 0 && (
                                <span>{ss.peers_connected}p</span>
                              )}
                              <span className="ml-auto font-mono text-gray-600">
                                {formatBytes(ss.downloaded_bytes)} / {formatBytes(ss.total_size_bytes)}
                              </span>
                            </div>
                          )}

                          {/* Error message */}
                          {isError && ss?.error_message && (
                            <p className="text-[9px] text-red-600 truncate mb-1" title={ss.error_message}>
                              {ss.error_message}
                            </p>
                          )}

                          {/* Last seen for seeding/complete */}
                          {(isSeeding || isComplete) && ss?.last_seen && (
                            <div className="text-[9px] text-gray-400">
                              Last seen {formatRelativeTime(ss.last_seen)}
                            </div>
                          )}

                          {/* Location badges for content that exists */}
                          {ss && !isMissing && (ss.ingestion_status || ss.download_path || ss.rosettabridge_path) && (
                            <div className="flex items-center gap-1.5 mt-1">
                              {ss.download_path && (
                                <span className="inline-flex items-center gap-1 text-[9px] px-1.5 py-0.5 rounded bg-gray-100 text-gray-600">
                                  <FolderDown className="h-2.5 w-2.5" />
                                  Download
                                </span>
                              )}
                              {ss.rosettabridge_path && (
                                <span className="inline-flex items-center gap-1 text-[9px] px-1.5 py-0.5 rounded bg-purple-100 text-purple-700">
                                  <Link2 className="h-2.5 w-2.5" />
                                  RosettaBridge
                                </span>
                              )}
                              {ss.ingestion_status && ss.ingestion_status !== 'downloaded' && (
                                <span className={cn(
                                  "text-[9px] px-1.5 py-0.5 rounded",
                                  ss.ingestion_status === 'seeding_switched' || ss.ingestion_status === 'cleanup_done'
                                    ? "bg-purple-100 text-purple-700"
                                    : ss.ingestion_status === 'verified'
                                      ? "bg-blue-100 text-blue-700"
                                      : ss.ingestion_status === 'failed'
                                        ? "bg-red-100 text-red-700"
                                        : "bg-gray-100 text-gray-600"
                                )}>
                                  {ss.ingestion_status === 'seeding_switched' ? 'Ingested'
                                    : ss.ingestion_status === 'cleanup_done' ? 'Ingested & Cleaned'
                                    : ss.ingestion_status === 'verified' ? 'Verified'
                                    : ss.ingestion_status === 'detected' ? 'Detected'
                                    : ss.ingestion_status === 'failed' ? 'Ingest Failed'
                                    : ss.ingestion_status}
                                </span>
                              )}
                            </div>
                          )}

                          {/* Inline action buttons for active transfers */}
                          {!isMissing && (isActive || isPaused || isError) && (
                            <div className="flex items-center gap-1 mt-1.5 pt-1.5 border-t border-gray-200" onClick={e => e.stopPropagation()}>
                              {isActive && hasTransfer && (
                                <button
                                  onClick={() => pauseMutation.mutate(ss!.transfer_id!)}
                                  className="flex items-center gap-1 rounded px-2 py-0.5 text-[9px] font-medium text-orange-700 bg-orange-100 hover:bg-orange-200 transition-colors"
                                >
                                  <Pause className="h-2.5 w-2.5" /> Pause
                                </button>
                              )}
                              {isPaused && hasTransfer && (
                                <button
                                  onClick={() => resumeMutation.mutate(ss!.transfer_id!)}
                                  className="flex items-center gap-1 rounded px-2 py-0.5 text-[9px] font-medium text-green-700 bg-green-100 hover:bg-green-200 transition-colors"
                                >
                                  <Play className="h-2.5 w-2.5" /> Resume
                                </button>
                              )}
                              {(isActive || isPaused) && hasTransfer && (
                                <button
                                  onClick={() => cancelMutation.mutate({ id: ss!.transfer_id!, deleteData: true })}
                                  className="flex items-center gap-1 rounded px-2 py-0.5 text-[9px] font-medium text-red-700 bg-red-100 hover:bg-red-200 transition-colors"
                                >
                                  <X className="h-2.5 w-2.5" /> Cancel
                                </button>
                              )}
                              {isError && selectedTorrent && (
                                <button
                                  onClick={() => retryMutation.mutate(server.id)}
                                  disabled={retryMutation.isPending}
                                  className="flex items-center gap-1 rounded px-2 py-0.5 text-[9px] font-medium text-blue-700 bg-blue-100 hover:bg-blue-200 transition-colors"
                                >
                                  <RotateCcw className="h-2.5 w-2.5" /> Retry
                                </button>
                              )}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>
          </>
      </div>
      )}

      {/* Bulk Delete Confirmation Dialog */}
      {deleteDialog.open && deleteDialog.serverStatuses.length > 0 && selectedDCP && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
          onClick={() => setDeleteDialog({ open: false, serverStatuses: [], adminPassword: '', hasRosettaBridge: false })}
        >
          <div
            className="w-full max-w-md rounded-xl border border-gray-200 bg-white shadow-xl mx-4"
            onClick={e => e.stopPropagation()}
          >
            <div className="border-b border-gray-200 px-5 py-4">
              <h3 className="text-base font-semibold text-gray-900">Delete Content</h3>
              <p className="text-sm text-gray-500 mt-1 truncate">{selectedDCP.package_name}</p>
            </div>
            <div className="px-5 py-4 space-y-3">
              <p className="text-sm text-gray-700">
                Are you sure you want to delete this DCP from <strong>{deleteDialog.serverStatuses.length}</strong> server{deleteDialog.serverStatuses.length > 1 ? 's' : ''}?
              </p>
              <div className="bg-gray-50 rounded p-3 space-y-1.5">
                {deleteDialog.serverStatuses.map(ss => {
                  const dr = deleteResults[ss.server_id];
                  return (
                    <div key={ss.server_id} className="flex items-center justify-between text-xs">
                      <div className="flex items-center gap-2">
                        {dr ? (
                          dr.status === 'pending' ? <Loader2 className="h-3 w-3 text-blue-500 animate-spin shrink-0" />
                          : dr.status === 'success' ? <CheckCircle2 className="h-3 w-3 text-green-500 shrink-0" />
                          : <AlertTriangle className="h-3 w-3 text-red-500 shrink-0" />
                        ) : null}
                        <span className="text-gray-700">{ss.server_name}</span>
                        {ss.rosettabridge_path && (
                          <span className="inline-flex items-center gap-0.5 text-[9px] px-1.5 py-0.5 rounded bg-purple-100 text-purple-700">
                            <Link2 className="h-2 w-2" />
                            RB
                          </span>
                        )}
                        {ss.download_path && (
                          <span className="inline-flex items-center gap-0.5 text-[9px] px-1.5 py-0.5 rounded bg-gray-100 text-gray-600">
                            <FolderDown className="h-2 w-2" />
                            DL
                          </span>
                        )}
                      </div>
                      <div className="flex items-center gap-2">
                        {dr ? (
                          <span className={cn(
                            "text-[9px] font-medium truncate max-w-[150px]",
                            dr.status === 'success' && "text-green-600",
                            dr.status === 'error' && "text-red-600",
                            dr.status === 'pending' && "text-blue-500",
                          )} title={dr.message}>
                            {dr.message}
                          </span>
                        ) : (
                          <span className="text-gray-500 font-mono">{formatBytes(ss.total_size_bytes)}</span>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>

              {deleteDialog.hasRosettaBridge && (
                <div className="bg-purple-50 border border-purple-200 rounded p-3">
                  <div className="flex items-center gap-2 mb-2">
                    <Link2 className="h-4 w-4 text-purple-600" />
                    <span className="text-xs font-semibold text-purple-800">RosettaBridge Protected Content</span>
                  </div>
                  <p className="text-xs text-purple-700 mb-2">
                    Some content is stored in a RosettaBridge location. Admin password required to delete.
                  </p>
                  <input
                    type="password"
                    placeholder="Admin password"
                    value={deleteDialog.adminPassword}
                    onChange={e => setDeleteDialog({ ...deleteDialog, adminPassword: e.target.value })}
                    className="w-full h-8 px-3 text-sm border border-purple-300 rounded bg-white focus:outline-none focus:ring-1 focus:ring-purple-500"
                  />
                </div>
              )}

              <p className="text-xs text-gray-500">
                This will permanently remove the content data from the selected servers.
                They will need new transfers to get the content back.
              </p>
            </div>
            <div className="border-t border-gray-200 px-5 py-3 flex justify-end gap-2">
              <button
                onClick={() => { setDeleteDialog({ open: false, serverStatuses: [], adminPassword: '', hasRosettaBridge: false }); setDeleteResults({}); }}
                disabled={bulkDeleteMutation.isPending}
                className={cn(
                  "rounded-md px-3 py-1.5 text-sm transition-colors",
                  bulkDeleteMutation.isPending
                    ? "text-gray-400 cursor-not-allowed"
                    : "text-gray-500 hover:text-gray-700 hover:bg-gray-100"
                )}
              >
                {bulkDeleteMutation.isSuccess ? 'Close' : 'Cancel'}
              </button>
              {!bulkDeleteMutation.isSuccess && (
                <button
                  onClick={() => bulkDeleteMutation.mutate(deleteDialog.serverStatuses)}
                  disabled={bulkDeleteMutation.isPending || (deleteDialog.hasRosettaBridge && deleteDialog.adminPassword !== 'password')}
                  className={cn(
                    "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
                    bulkDeleteMutation.isPending
                      ? "bg-red-400 text-white cursor-not-allowed"
                      : deleteDialog.hasRosettaBridge && deleteDialog.adminPassword !== 'password'
                        ? "bg-gray-300 text-gray-500 cursor-not-allowed"
                        : "bg-red-600 text-white hover:bg-red-700"
                  )}
                >
                  {bulkDeleteMutation.isPending ? (
                    <><Loader2 className="h-3.5 w-3.5 animate-spin" /> Deleting...</>
                  ) : (
                    <><Trash2 className="h-3.5 w-3.5" /> Delete from {deleteDialog.serverStatuses.length} server{deleteDialog.serverStatuses.length > 1 ? 's' : ''}</>
                  )}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
