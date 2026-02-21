import { useTorrents } from '@/hooks/useTorrents';
import { useTorrentQueue } from '@/hooks/useTorrentQueue';
import { useServers } from '@/hooks/useServers';
import { useDebounce } from '@/hooks/useDebounce';
import { formatBytes, formatRelativeTime, formatDurationSince, formatETA, formatSpeed } from '@/utils/format';
import { Copy, Check, Hash, FileArchive, Link2, Clock, AlertCircle, Loader2, CheckCircle, XCircle, RotateCcw, ScanSearch, Timer, HardDrive, Layers, Zap, Server as ServerIcon, X, Film, Inbox, Search } from 'lucide-react';
import { useState, useMemo, useEffect, useRef } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Badge } from '@/components/ui/badge';
import { Progress } from '@/components/ui/progress';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Button } from '@/components/ui/button';
import { torrentsApi } from '@/api/torrents';
import { serversApi, serverDisplayName, type ScanStatus } from '@/api/servers';
import type { Server, TorrentQueueItem } from '@/api/types';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog';

function formatElapsed(startedAt: string | undefined): string {
  if (!startedAt) return '';
  const now = Date.now();
  const then = new Date(startedAt).getTime();
  if (Number.isNaN(then)) return '';
  const diff = Math.floor((now - then) / 1000);
  const d = Math.floor(diff / 86400);
  const h = Math.floor((diff % 86400) / 3600);
  const m = Math.floor((diff % 3600) / 60);
  const s = Math.floor(diff % 60);
  if (d > 0) return `${d}d ${h}h ${m}m`;
  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

function HashingProgressCard({ item }: { item: TorrentQueueItem & { queue_position: number; eta_seconds?: number } }) {
  const [elapsed, setElapsed] = useState(() => formatElapsed(item.started_at));
  const [showCancelDialog, setShowCancelDialog] = useState(false);
  const queryClient = useQueryClient();

  const cancelMutation = useMutation({
    mutationFn: () => torrentsApi.cancelQueueItem(item.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['torrent-queue'] });
      setShowCancelDialog(false);
    },
  });

  useEffect(() => {
    if (!item.started_at) return;
    setElapsed(formatElapsed(item.started_at));
    const interval = setInterval(() => setElapsed(formatElapsed(item.started_at)), 1000);
    return () => clearInterval(interval);
  }, [item.started_at]);

  const bytesHashed = item.bytes_hashed ?? (item.total_size_bytes != null ? Math.round(item.total_size_bytes * item.progress_percent / 100) : null);
  const piecesHashed = item.pieces_hashed ?? 0;
  const totalPieces = item.total_pieces ?? 0;

  // Determine phase from current_file text
  const isReadingPhase = item.current_file?.startsWith('Reading ');
  const isHashingPhase = item.current_file?.startsWith('Hashing piece');
  const phaseLabel = isReadingPhase ? 'Reading files' : isHashingPhase ? 'Verifying' : 'Processing';

  return (
    <>
      <div className="p-5 border-b border-border last:border-b-0 hover:bg-accent/20 transition-colors">
        {/* Header row */}
        <div className="flex items-center justify-between gap-4 mb-3">
          <div className="flex items-center gap-2 min-w-0">
            <Badge variant="default" className="bg-blue-500 shrink-0">
              <Loader2 className="h-3 w-3 mr-1 animate-spin" />Ingesting
            </Badge>
            <span className="text-xs text-muted-foreground shrink-0">#{item.queue_position}</span>
            <span className="text-xs text-muted-foreground flex items-center gap-1 shrink-0">
              <ServerIcon className="h-3 w-3" />
              {item.server_name || 'Unknown Server'}
            </span>
          </div>
          <div className="flex items-center gap-2">
            {elapsed && (
              <div className="flex items-center gap-1 text-xs text-muted-foreground shrink-0">
                <Timer className="h-3 w-3" />
                {elapsed}
              </div>
            )}
            <Button
              variant="outline"
              size="sm"
              onClick={() => setShowCancelDialog(true)}
              disabled={cancelMutation.isPending}
              className="h-7 px-2 text-xs shrink-0"
            >
              <X className="h-3 w-3 mr-1" />
              Cancel
            </Button>
          </div>
        </div>

      {/* Package name */}
      <div className="font-semibold text-foreground truncate mb-1 text-base">{item.package_name}</div>

      {/* Current file / phase */}
      {item.current_file && (
        <div className="text-xs text-muted-foreground truncate mb-3 font-mono bg-muted/50 rounded px-2 py-1">
          {item.current_file}
        </div>
      )}

      {/* Main progress bar */}
      <div className="mb-3">
        <div className="flex items-center justify-between mb-1.5">
          <span className="text-xs font-medium text-foreground">{phaseLabel}</span>
          <span className="text-sm font-mono font-semibold text-foreground tabular-nums">
            {item.progress_percent.toFixed(1)}%
          </span>
        </div>
        <div className="relative">
          <Progress value={item.progress_percent} className="h-3" />
        </div>
      </div>

      {/* Stats grid */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {/* Bytes hashed */}
        <div className="bg-muted/40 rounded-lg px-3 py-2">
          <div className="flex items-center gap-1.5 mb-0.5">
            <HardDrive className="h-3 w-3 text-muted-foreground" />
            <span className="text-[10px] uppercase tracking-wider text-muted-foreground font-medium">Processed</span>
          </div>
          <div className="text-sm font-mono font-medium text-foreground tabular-nums">
            {bytesHashed != null ? formatBytes(bytesHashed) : '--'}
            {item.total_size_bytes != null && (
              <span className="text-muted-foreground text-xs"> / {formatBytes(item.total_size_bytes)}</span>
            )}
          </div>
        </div>

        {/* Blocks (ingestion segments) */}
        <div className="bg-muted/40 rounded-lg px-3 py-2">
          <div className="flex items-center gap-1.5 mb-0.5">
            <Layers className="h-3 w-3 text-muted-foreground" />
            <span className="text-[10px] uppercase tracking-wider text-muted-foreground font-medium">Blocks</span>
          </div>
          <div className="text-sm font-mono font-medium text-foreground tabular-nums">
            {totalPieces > 0 ? (
              <>{piecesHashed.toLocaleString()} <span className="text-muted-foreground text-xs">/ {totalPieces.toLocaleString()}</span></>
            ) : '--'}
          </div>
        </div>

        {/* Speed */}
        <div className="bg-muted/40 rounded-lg px-3 py-2">
          <div className="flex items-center gap-1.5 mb-0.5">
            <Zap className="h-3 w-3 text-muted-foreground" />
            <span className="text-[10px] uppercase tracking-wider text-muted-foreground font-medium">Speed</span>
          </div>
          <div className="text-sm font-mono font-medium text-foreground tabular-nums">
            {item.hashing_speed_bps != null && item.hashing_speed_bps > 0 ? formatSpeed(item.hashing_speed_bps) : '--'}
          </div>
        </div>

        {/* ETA */}
        <div className="bg-muted/40 rounded-lg px-3 py-2">
          <div className="flex items-center gap-1.5 mb-0.5">
            <Clock className="h-3 w-3 text-muted-foreground" />
            <span className="text-[10px] uppercase tracking-wider text-muted-foreground font-medium">ETA</span>
          </div>
          <div className="text-sm font-mono font-medium text-foreground tabular-nums">
            {item.eta_seconds != null && item.eta_seconds > 0 ? formatETA(item.eta_seconds) : '--'}
          </div>
        </div>
      </div>
    </div>

    {/* Cancel confirmation dialog */}
    <Dialog open={showCancelDialog} onOpenChange={setShowCancelDialog}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cancel ingestion?</DialogTitle>
          <DialogDescription>
            Are you sure you want to cancel the ingestion of <strong>{item.package_name}</strong>?
            <br /><br />
            Progress will be saved as a checkpoint, so you can retry later without starting from scratch.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => setShowCancelDialog(false)} disabled={cancelMutation.isPending}>
            Continue ingesting
          </Button>
          <Button
            variant="destructive"
            onClick={() => cancelMutation.mutate()}
            disabled={cancelMutation.isPending}
          >
            {cancelMutation.isPending ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                Cancelling...
              </>
            ) : (
              <>
                <X className="mr-2 h-4 w-4" />
                Cancel ingestion
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </>
  );
}

export default function Torrents() {
  const queryClient = useQueryClient();
  const { data: torrentsRaw, isLoading } = useTorrents();
  const { data: queueRaw, isLoading: queueLoading } = useTorrentQueue();
  const { data: serversRaw } = useServers();
  const [copied, setCopied] = useState<string | null>(null);

  const retryMutation = useMutation({
    mutationFn: (queueItemId: string) => torrentsApi.retryQueueItem(queueItemId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['torrent-queue'] });
    },
  });

  const [rescanOpen, setRescanOpen] = useState(false);
  const [selectedServerIds, setSelectedServerIds] = useState<Set<string>>(new Set());
  const [rescanningIds, setRescanningIds] = useState<Set<string>>(new Set());
  const [scanStatusByServer, setScanStatusByServer] = useState<Record<string, ScanStatus>>({});
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const torrents = Array.isArray(torrentsRaw) ? torrentsRaw : [];
  const queueItems = Array.isArray(queueRaw) ? queueRaw : [];
  const servers = Array.isArray(serversRaw) ? serversRaw : [];
  const activeServers = useMemo(() => servers.filter((s: Server) => s.is_authorized), [servers]);

  const [catalogSearch, setCatalogSearch] = useState('');
  const [catalogLimit, setCatalogLimit] = useState(100);
  const debouncedCatalogSearch = useDebounce(catalogSearch, 300);

  const filteredTorrents = useMemo(() => {
    if (!debouncedCatalogSearch) return torrents;
    const q = debouncedCatalogSearch.toLowerCase();
    return torrents.filter(t =>
      (t.package_name || '').toLowerCase().includes(q) ||
      (t.content_title || '').toLowerCase().includes(q) ||
      (t.info_hash || '').toLowerCase().includes(q)
    );
  }, [torrents, debouncedCatalogSearch]);

  const visibleTorrents = filteredTorrents.slice(0, catalogLimit);

  const toggleServerSelection = (id: string) => {
    setSelectedServerIds(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const selectAllServers = () => {
    setSelectedServerIds(new Set(activeServers.map((s: Server) => s.id)));
  };
  const startRescan = async () => {
    const ids = selectedServerIds.size ? Array.from(selectedServerIds) : activeServers.map((s: Server) => s.id);
    setRescanningIds(new Set(ids));
    setScanStatusByServer({});
    try {
      await Promise.all(ids.map((id: string) => serversApi.rescan(id)));
    } catch (e) {
      console.error('Rescan trigger failed', e);
    }
  };
  useEffect(() => {
    const ids = Array.from(rescanningIds);
    if (ids.length === 0) return;
    const fetch = async () => {
      const updates: Record<string, ScanStatus> = {};
      for (const id of ids) {
        try {
          const status = await serversApi.getScanStatus(id);
          updates[id] = status;
        } catch {
          updates[id] = { status: 'error', message: 'Failed to fetch status' };
        }
      }
      setScanStatusByServer(prev => ({ ...prev, ...updates }));
    };
    fetch();
    pollRef.current = setInterval(fetch, 2500);
    return () => {
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [rescanningIds]);
  useEffect(() => {
    const ids = Array.from(rescanningIds);
    const finished = (s: ScanStatus) => s && ['success', 'partial', 'failed', 'error'].includes(s.status);
    const done = ids.length > 0 && ids.every(id => finished(scanStatusByServer[id]));
    if (done && pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
      setRescanningIds(new Set());
      queryClient.invalidateQueries({ queryKey: ['torrent-queue'] });
      queryClient.invalidateQueries({ queryKey: ['torrents'] });
    }
  }, [rescanningIds, scanStatusByServer, queryClient]);

  const serverNameMap = useMemo(() =>
    servers.reduce((acc, s) => ({ ...acc, [s.id]: serverDisplayName(s) }), {} as Record<string, string>),
    [servers]
  );

  // Separate queue items by status
  const generatingItems = useMemo(() => {
    return queueItems
      .filter(item => item.status === 'generating')
      .map((item, idx) => ({
        ...item,
        queue_position: idx + 1,
        // Use backend-provided ETA, or calculate from speed
        eta_seconds: item.eta_seconds ?? (
          item.hashing_speed_bps && item.hashing_speed_bps > 0 && item.total_size_bytes
            ? Math.round((item.total_size_bytes * (100 - item.progress_percent) / 100) / item.hashing_speed_bps)
            : undefined
        ),
      }));
  }, [queueItems]);

  const queuedItems = useMemo(() => {
    return queueItems
      .filter(item => item.status === 'queued')
      .map((item, idx) => ({
        ...item,
        queue_position: idx + 1,
      }));
  }, [queueItems]);

  const failedItems = useMemo(() => {
    return queueItems
      .filter(item => item.status === 'failed')
      .map((item, idx) => ({
        ...item,
        queue_position: idx + 1,
      }));
  }, [queueItems]);

  const completedItems = useMemo(() => {
    return queueItems
      .filter(item => item.status === 'completed')
      .map((item, idx) => ({
        ...item,
        queue_position: idx + 1,
      }));
  }, [queueItems]);

  const copyHash = (hash: string) => {
    navigator.clipboard.writeText(hash);
    setCopied(hash);
    setTimeout(() => setCopied(null), 2000);
  };

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'generating':
        return <Badge variant="default" className="bg-blue-500"><Loader2 className="h-3 w-3 mr-1 animate-spin" />Ingesting</Badge>;
      case 'queued':
        return <Badge variant="secondary"><Clock className="h-3 w-3 mr-1" />Queued</Badge>;
      case 'completed':
        return <Badge variant="default" className="bg-green-500"><CheckCircle className="h-3 w-3 mr-1" />Completed</Badge>;
      case 'failed':
        return <Badge variant="destructive"><XCircle className="h-3 w-3 mr-1" />Failed</Badge>;
      default:
        return <Badge variant="outline">{status}</Badge>;
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Ingesting</h1>
        <p className="text-sm text-muted-foreground mt-0.5">
          {torrents.length} packages ready • {queueItems.length} in queue
          {generatingItems.length > 0 && <span className="text-blue-500 ml-1">• {generatingItems.length} ingesting</span>}
        </p>
      </div>

      {/* Ingest queue with tabs */}
      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="px-4 py-3 border-b border-border bg-muted/30 flex items-start justify-between gap-4">
          <div>
            <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
              <Hash className="h-4 w-4" />
              Ingest queue
            </h2>
            <p className="text-xs text-muted-foreground mt-1">In progress, queued, failed, and completed ingestion jobs — updates every 10s</p>
          </div>
          <Button variant="outline" size="sm" onClick={() => setRescanOpen(true)} className="shrink-0">
            <ScanSearch className="h-3 w-3 mr-1" />
            Rescan library
          </Button>
        </div>

        {/* Rescan dialog */}
        <Dialog open={rescanOpen} onOpenChange={setRescanOpen}>
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle>Rescan library</DialogTitle>
              <DialogDescription>
                Trigger a full library scan on selected servers. Progress is reported from each server to the main server.
              </DialogDescription>
            </DialogHeader>
            <div className="space-y-4 py-2">
              {activeServers.length === 0 ? (
                <p className="text-sm text-muted-foreground">No active servers. Authorize servers in Sites first.</p>
              ) : (
                <>
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-medium text-foreground">Servers</span>
                    <Button variant="ghost" size="sm" onClick={selectAllServers}>Select all</Button>
                  </div>
                  <ul className="space-y-2 max-h-48 overflow-y-auto rounded-md border border-border p-2">
                    {activeServers.map((s: Server) => (
                      <li key={s.id} className="flex items-center gap-2">
                        <input
                          type="checkbox"
                          id={`rescan-${s.id}`}
                          checked={selectedServerIds.has(s.id)}
                          onChange={() => toggleServerSelection(s.id)}
                          className="rounded border-border"
                        />
                        <label htmlFor={`rescan-${s.id}`} className="text-sm cursor-pointer truncate">{s.name}</label>
                      </li>
                    ))}
                  </ul>
                  {rescanningIds.size > 0 && (
                    <div className="space-y-3 pt-2 border-t border-border">
                      <p className="text-xs font-medium text-foreground">Progress</p>
                      {Array.from(rescanningIds).map((id: string) => {
                        const status = scanStatusByServer[id];
                        const name = serverNameMap[id] || id;
                        const isRunning = status?.status === 'running';
                        const progress = status?.packages_found
                          ? Math.round((100 * ((status.packages_added ?? 0) + (status.packages_updated ?? 0))) / status.packages_found)
                          : 0;
                        return (
                          <div key={id} className="text-sm">
                            <div className="flex justify-between mb-1">
                              <span className="font-medium truncate">{name}</span>
                              <span className="text-muted-foreground capitalize">{status?.status ?? '...'}</span>
                            </div>
                            {isRunning && (
                              <Progress value={Math.min(progress, 99)} className="h-1.5" />
                            )}
                            {status?.packages_found != null && (
                              <p className="text-xs text-muted-foreground mt-1">
                                {status.packages_added ?? 0} added, {status.packages_updated ?? 0} updated of {status.packages_found} found
                              </p>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  )}
                </>
              )}
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => setRescanOpen(false)}>Cancel</Button>
              <Button
                onClick={startRescan}
                disabled={activeServers.length === 0 || rescanningIds.size > 0}
              >
                {rescanningIds.size > 0 ? (
                  <><Loader2 className="h-3 w-3 mr-1 animate-spin" /> Rescanning...</>
                ) : (
                  'Start rescan'
                )}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>

        <Tabs defaultValue="in-progress" className="w-full">
          <div className="px-4 pt-3 border-b border-border">
            <TabsList className="w-full justify-start h-auto p-0 bg-transparent gap-1">
              <TabsTrigger value="in-progress" className="data-[state=active]:bg-primary/10 data-[state=active]:text-primary rounded-md px-3 py-2">
                In progress
                <Badge variant="secondary" className="ml-1.5 text-xs">{generatingItems.length}</Badge>
              </TabsTrigger>
              <TabsTrigger value="queued" className="data-[state=active]:bg-primary/10 data-[state=active]:text-primary rounded-md px-3 py-2">
                Queued
                <Badge variant="secondary" className="ml-1.5 text-xs">{queuedItems.length}</Badge>
              </TabsTrigger>
              <TabsTrigger value="failed" className="data-[state=active]:bg-primary/10 data-[state=active]:text-primary rounded-md px-3 py-2">
                Failed
                <Badge variant="secondary" className="ml-1.5 text-xs">{failedItems.length}</Badge>
              </TabsTrigger>
              <TabsTrigger value="successful" className="data-[state=active]:bg-primary/10 data-[state=active]:text-primary rounded-md px-3 py-2">
                Successful
                <Badge variant="secondary" className="ml-1.5 text-xs">{completedItems.length}</Badge>
              </TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="in-progress" className="mt-0">
            {generatingItems.length === 0 ? (
              <div className="p-8 text-center text-muted-foreground">
                <Loader2 className="h-8 w-8 mx-auto mb-3 opacity-50" />
                <p className="text-sm">No items ingesting right now</p>
              </div>
            ) : (
              generatingItems.map(item => (
                <HashingProgressCard key={item.id} item={item} />
              ))
            )}
          </TabsContent>

          <TabsContent value="queued" className="mt-0">
            {queuedItems.length === 0 ? (
              <div className="p-12 text-center text-muted-foreground">
                <div className="inline-flex h-14 w-14 items-center justify-center rounded-full bg-muted/60 mb-4">
                  <Inbox className="h-7 w-7 opacity-60" />
                </div>
                <p className="text-sm font-medium text-foreground">No items in queue</p>
                <p className="text-xs mt-1 max-w-xs mx-auto">DCPs waiting to be ingested will appear here</p>
              </div>
            ) : (
              <div className="p-4 max-h-[70vh] overflow-y-auto">
                <div className="grid gap-3">
                  {queuedItems.map((item, index) => (
                    <div
                      key={item.id}
                      className="group relative flex items-start gap-4 rounded-lg border border-border/80 bg-card px-4 py-3.5 shadow-sm transition-all hover:border-primary/20 hover:shadow-md hover:bg-accent/5"
                      title={item.package_name}
                    >
                      {/* Position indicator */}
                      <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-sm font-bold text-primary ring-1 ring-primary/10">
                        {item.queue_position ?? index + 1}
                      </div>
                      {/* Content */}
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2 mb-1.5">
                          <Badge variant="secondary" className="text-xs font-normal gap-1 shrink-0">
                            <Clock className="h-3 w-3" />
                            Queued
                          </Badge>
                          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground shrink-0">
                            <ServerIcon className="h-3 w-3" />
                            {item.server_name || 'Unknown Server'}
                          </span>
                        </div>
                        <p className="font-medium text-foreground text-sm leading-snug truncate mb-1.5" title={item.package_name}>
                          {item.package_name || 'Unknown package'}
                        </p>
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
                          <span>DCP discovered {formatRelativeTime(item.queued_at)} · in queue for {formatDurationSince(item.queued_at)}</span>
                          {item.total_size_bytes != null && (
                            <>
                              <span className="text-border">·</span>
                              <span className="inline-flex items-center gap-1">
                                <Film className="h-3 w-3 opacity-70" />
                                {formatBytes(item.total_size_bytes)}
                              </span>
                            </>
                          )}
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </TabsContent>

          <TabsContent value="failed" className="mt-0">
            <div className="divide-y divide-border">
            {failedItems.length === 0 ? (
              <div className="p-8 text-center text-muted-foreground">
                <CheckCircle className="h-8 w-8 mx-auto mb-3 opacity-50 text-green-500" />
                <p className="text-sm">No failed items</p>
              </div>
            ) : failedItems.map(item => (
              <div key={item.id} className="p-4 hover:bg-accent/30 transition-colors bg-destructive/5">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-2">
                      {getStatusBadge(item.status)}
                      <span className="text-xs text-muted-foreground flex items-center gap-1">
                        <ServerIcon className="h-3 w-3" />
                        {item.server_name || 'Unknown Server'}
                      </span>
                    </div>
                    <div className="font-medium text-foreground truncate mb-1">{item.package_name}</div>
                    {item.error_message && (
                      <div className="text-xs text-destructive mt-1 bg-destructive/10 rounded px-2 py-1.5 break-words">{item.error_message}</div>
                    )}
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    className="shrink-0"
                    onClick={() => retryMutation.mutate(item.id)}
                    disabled={retryMutation.isPending}
                  >
                    {retryMutation.isPending ? <Loader2 className="h-3 w-3 animate-spin" /> : <RotateCcw className="h-3 w-3 mr-1" />}
                    Retry
                  </Button>
                </div>
              </div>
            ))}
            </div>
          </TabsContent>

          <TabsContent value="successful" className="mt-0">
            <div className="divide-y divide-border">
            {completedItems.length === 0 ? (
              <div className="p-8 text-center text-muted-foreground">
                <CheckCircle className="h-8 w-8 mx-auto mb-3 opacity-50 text-green-500" />
                <p className="text-sm">No completed items in queue</p>
                <p className="text-xs mt-1">Finished ingestion jobs appear in the catalog below</p>
              </div>
            ) : completedItems.map(item => (
              <div key={item.id} className="p-4 hover:bg-accent/30 transition-colors">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-2">
                      {getStatusBadge(item.status)}
                      <span className="text-xs text-muted-foreground flex items-center gap-1">
                        <ServerIcon className="h-3 w-3" />
                        {item.server_name || 'Unknown Server'}
                      </span>
                    </div>
                    <div className="font-medium text-foreground truncate">{item.package_name}</div>
                    {item.completed_at && (
                      <div className="text-xs text-muted-foreground mt-1">Completed {formatRelativeTime(item.completed_at)}</div>
                    )}
                  </div>
                </div>
              </div>
            ))}
            </div>
          </TabsContent>
        </Tabs>

        {queueLoading && (
          <div className="p-8 text-center text-muted-foreground border-t border-border">
            <Loader2 className="h-8 w-8 mx-auto mb-3 animate-spin" />
            <p className="text-sm">Loading queue...</p>
          </div>
        )}
      </div>

      {/* Ingested catalog */}
      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="px-4 py-3 border-b border-border bg-muted/30 flex items-start justify-between gap-4">
          <div>
            <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
              <Link2 className="h-4 w-4" />
              Ingested catalog
              <span className="text-xs font-normal text-muted-foreground">
                ({visibleTorrents.length}{filteredTorrents.length > visibleTorrents.length ? `/${filteredTorrents.length}` : ''} of {torrents.length})
              </span>
            </h2>
            <p className="text-xs text-muted-foreground mt-1">DCPs ready for distribution</p>
          </div>
          <div className="relative shrink-0">
            <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              value={catalogSearch}
              onChange={e => { setCatalogSearch(e.target.value); setCatalogLimit(100); }}
              placeholder="Search catalog..."
              className="h-8 w-48 rounded border border-border bg-background pl-8 pr-3 text-xs focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground">DCP Package</th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground">Content ID</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Size</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Blocks</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Block size</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Files</th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground">Source</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Created</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading && Array.from({ length: 5 }).map((_, i) => (
                <tr key={i}>
                  {Array.from({ length: 8 }).map((_, j) => (
                    <td key={j} className="px-4 py-3"><div className="h-4 w-3/4 animate-pulse rounded bg-muted" /></td>
                  ))}
                </tr>
              ))}
              {!isLoading && torrents.length === 0 && (
                <tr><td colSpan={8} className="px-4 py-16 text-center text-muted-foreground">
                  <FileArchive className="mx-auto h-10 w-10 mb-3 opacity-30" />
                  <p className="font-medium text-foreground">No packages ingested yet</p>
                  <p className="text-xs mt-1">DCPs will appear here after ingestion</p>
                </td></tr>
              )}
              {!isLoading && torrents.length > 0 && filteredTorrents.length === 0 && (
                <tr><td colSpan={8} className="px-4 py-8 text-center text-muted-foreground text-xs">
                  No results for &ldquo;{catalogSearch}&rdquo;
                </td></tr>
              )}
              {visibleTorrents.map(t => (
                <tr key={t.id} className="hover:bg-accent/30 transition-colors">
                  <td className="px-4 py-3">
                    <div className="flex flex-col gap-0.5">
                      <span className="font-medium text-foreground">{t.content_title || t.package_name || 'Unknown Package'}</span>
                      {t.content_title && t.package_name && t.content_title !== t.package_name && <span className="text-xs text-muted-foreground truncate">{t.package_name}</span>}
                    </div>
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex items-center gap-1.5 min-w-fit">
                      <Hash className="h-3 w-3 text-muted-foreground shrink-0" />
                      <code className="text-xs font-mono text-primary truncate">{t.info_hash}</code>
                      <button
                        onClick={() => copyHash(t.info_hash)}
                        className="text-muted-foreground hover:text-foreground transition-colors shrink-0 p-0.5 rounded hover:bg-accent"
                        title="Copy content ID"
                      >
                        {copied === t.info_hash ? <Check className="h-3 w-3 text-success" /> : <Copy className="h-3 w-3" />}
                      </button>
                    </div>
                  </td>
                  <td className="px-4 py-3 text-right font-mono text-foreground text-xs">{formatBytes(t.total_size_bytes)}</td>
                  <td className="px-4 py-3 text-right text-muted-foreground tabular-nums">{t.total_pieces.toLocaleString()}</td>
                  <td className="px-4 py-3 text-right text-muted-foreground text-xs">{formatBytes(t.piece_size)}</td>
                  <td className="px-4 py-3 text-right text-muted-foreground text-xs">{t.file_count}</td>
                  <td className="px-4 py-3 text-muted-foreground text-xs">{serverNameMap[t.created_by_server_id] || '\u2014'}</td>
                  <td className="px-4 py-3 text-right text-muted-foreground whitespace-nowrap text-xs">{formatRelativeTime(t.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {filteredTorrents.length > visibleTorrents.length && (
            <div className="px-4 py-3 text-center border-t border-border">
              <button
                onClick={() => setCatalogLimit(prev => prev + 100)}
                className="text-xs text-primary hover:underline font-medium"
              >
                Load more ({filteredTorrents.length - visibleTorrents.length} remaining)
              </button>
            </div>
          )}
        </div>
      </div>

      {/* Info Section */}
      <div className="rounded-xl border border-warning/30 bg-warning/5 p-4">
        <div className="flex gap-3">
          <AlertCircle className="h-5 w-5 text-warning shrink-0 mt-0.5" />
          <div className="text-sm">
            <p className="font-medium text-warning">Ingesting</p>
            <p className="text-xs text-muted-foreground mt-1">
              DCPs are ingested when discovered: files are read and verified so they can be distributed. Progress updates every 10 seconds from each server.
              Jobs stuck for more than 3 hours are automatically moved to Failed. Use the tabs above to monitor all ingestion activity.
            </p>
          </div>
        </div>
      </div>
    </div>
  );
}
