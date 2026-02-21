import { useState, useMemo, useEffect } from 'react';
import { useTorrents } from '@/hooks/useTorrents';
import { useTorrentQueue } from '@/hooks/useTorrentQueue';
import { useTransfers } from '@/hooks/useTransfers';
import { useServers } from '@/hooks/useServers';
import { useQuery } from '@tanstack/react-query';
import { torrentsApi } from '@/api/torrents';
import { transfersApi } from '@/api/transfers';
import { serverDisplayName } from '@/api/servers';
import { formatBytes, formatRelativeTime } from '@/utils/format';
import {
  Download,
  Copy,
  Check,
  Hash,
  FileArchive,
  Link2,
  ChevronDown,
  ChevronRight,
  RefreshCw,
  Users,
  ArrowLeftRight,
  ListTodo,
  Loader2,
  Radio,
  SatelliteDish,
  Activity,
  HardDrive,
  CheckCircle2,
  Clock,
  AlertCircle,
  Zap,
  TrendingUp,
  Server as ServerIcon,
  Search,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Progress } from '@/components/ui/progress';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible';
import { Input } from '@/components/ui/input';
import type { Torrent, Seeder, Transfer, ServerTorrentStatus, AnnounceAttempt, ServerTorrentStat } from '@/api/types';

/** Consider seeder "live" if last_announce is within this many ms (same as DCPLibrary) */
const SEEDER_LIVE_MS = 2 * 60 * 1000;

function isSeederLive(lastAnnounce: string): boolean {
  const t = new Date(lastAnnounce).getTime();
  return !isNaN(t) && Date.now() - t < SEEDER_LIVE_MS;
}

function formatSpeed(bytesPerSecond: number): string {
  if (bytesPerSecond === 0) return '—';
  // Convert bytes per second to megabits per second (mbps)
  // 1 byte = 8 bits, so multiply by 8 and divide by 1024*1024 to get mbps
  const mbps = (bytesPerSecond * 8) / (1024 * 1024);
  if (mbps < 1) {
    // For speeds less than 1 mbps, show in kbps
    const kbps = (bytesPerSecond * 8) / 1024;
    return `${kbps.toFixed(1)} kbps`;
  }
  return `${mbps.toFixed(1)} mbps`;
}

function formatETA(seconds?: number): string {
  if (!seconds || seconds <= 0) return '—';
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  const hours = Math.floor(seconds / 3600);
  const mins = Math.floor((seconds % 3600) / 60);
  return `${hours}h ${mins}m`;
}

function getStatusColor(status: string): string {
  switch (status) {
    case 'seeding':
      return 'bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20';
    case 'verifying':
      return 'bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20';
    case 'downloading':
      return 'bg-purple-500/10 text-purple-700 dark:text-purple-400 border-purple-500/20';
    case 'completed':
      return 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border-emerald-500/20';
    case 'error':
      return 'bg-red-500/10 text-red-700 dark:text-red-400 border-red-500/20';
    case 'paused':
      return 'bg-gray-500/10 text-gray-700 dark:text-gray-400 border-gray-500/20';
    default:
      return 'bg-muted text-muted-foreground border-border';
  }
}

function getStatusIcon(status: string) {
  switch (status) {
    case 'seeding':
      return <CheckCircle2 className="h-3.5 w-3.5" />;
    case 'verifying':
      return <Activity className="h-3.5 w-3.5 animate-pulse" />;
    case 'downloading':
      return <Download className="h-3.5 w-3.5" />;
    case 'completed':
      return <CheckCircle2 className="h-3.5 w-3.5" />;
    case 'error':
      return <AlertCircle className="h-3.5 w-3.5" />;
    default:
      return <Clock className="h-3.5 w-3.5" />;
  }
}

export default function TorrentStatus() {
  const { data: torrentsRaw, isLoading: torrentsLoading, isError: torrentsError, refetch: refetchTorrents } = useTorrents();
  const { data: queueRaw, isLoading: queueLoading } = useTorrentQueue();
  const { data: transfersRaw, isLoading: transfersLoading, isError: transfersError, refetch: refetchTransfers } = useTransfers();
  const { data: serversRaw } = useServers();

  // Fetch all server torrent stats
  const { data: allServerStats = [], isLoading: statsLoading, refetch: refetchStats } = useQuery({
    queryKey: ['torrent-stats-all'],
    queryFn: () => torrentsApi.getAllServersTorrentStats(),
    refetchInterval: 10000, // Poll every 10 seconds
  });

  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);
  const [dcpSearch, setDcpSearch] = useState('');

  const torrents = Array.isArray(torrentsRaw) ? torrentsRaw : [];

  // Filter torrents by DCP search (package name, content title, info hash)
  const filteredTorrents = useMemo(() => {
    const q = dcpSearch.trim().toLowerCase();
    if (!q) return torrents;
    return torrents.filter(
      t =>
        (t.package_name ?? '').toLowerCase().includes(q) ||
        (t.content_title ?? '').toLowerCase().includes(q) ||
        (t.info_hash ?? '').toLowerCase().includes(q)
    );
  }, [torrents, dcpSearch]);
  const queueItems = Array.isArray(queueRaw) ? queueRaw : [];
  const transfers = Array.isArray(transfersRaw) ? transfersRaw : [];
  const servers = Array.isArray(serversRaw) ? serversRaw : [];

  const serverNameMap = useMemo(
    () => servers.reduce((acc, s) => ({ ...acc, [s.id]: serverDisplayName(s) }), {} as Record<string, string>),
    [servers]
  );

  // Group stats by info_hash
  const statsByInfoHash = useMemo(() => {
    const map: Record<string, ServerTorrentStat[]> = {};
    allServerStats.forEach(stat => {
      if (!map[stat.info_hash]) map[stat.info_hash] = [];
      map[stat.info_hash].push(stat);
    });
    return map;
  }, [allServerStats]);

  // Summary counts
  const totalTorrents = torrents.length;
  const torrentsWithSeeders = torrents.filter(t => (t.seeders_count ?? 0) > 0).length;
  const activeTransfersCount = transfers.filter(t => t.status === 'downloading' || t.status === 'queued').length;
  const generatingCount = queueItems.filter(i => i.status === 'generating').length;
  const queuedCount = queueItems.filter(i => i.status === 'queued').length;

  // Verification stats
  const verifyingCount = allServerStats.filter(s => s.status === 'verifying').length;
  const seedingCount = allServerStats.filter(s => s.status === 'seeding').length;
  const totalPieces = allServerStats.reduce((sum, s) => sum + s.pieces_total, 0);
  const verifiedPieces = allServerStats.reduce((sum, s) => sum + s.pieces_completed, 0);
  const avgProgress = totalPieces > 0 ? (verifiedPieces / totalPieces) * 100 : 0;

  // Transfers per torrent (for table count)
  const transfersByTorrentId = useMemo(() => {
    const map: Record<string, Transfer[]> = {};
    transfers.forEach(tr => {
      if (!map[tr.torrent_id]) map[tr.torrent_id] = [];
      map[tr.torrent_id].push(tr);
    });
    return map;
  }, [transfers]);

  const copyHash = (hash: string) => {
    navigator.clipboard.writeText(hash);
    setCopied(hash);
    setTimeout(() => setCopied(null), 2000);
  };

  const handleRefresh = () => {
    refetchTorrents();
    refetchTransfers();
    refetchStats();
  };

  const loading = torrentsLoading;
  const error = torrentsError || transfersError;

  return (
    <div className="space-y-6 pb-8">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-3xl font-bold tracking-tight text-foreground">Transfer Status</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Real-time verification progress, registry, and distribution
          </p>
        </div>
        <Button variant="outline" size="default" onClick={handleRefresh} disabled={loading}>
          <RefreshCw className={`h-4 w-4 mr-2 ${loading ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {error && (
        <div className="rounded-xl border border-destructive/50 bg-destructive/5 px-4 py-3.5 text-sm text-destructive flex items-start gap-3">
          <AlertCircle className="h-5 w-5 mt-0.5 shrink-0" />
          <div>
            <p className="font-medium">Failed to load data</p>
            <p className="text-xs mt-0.5 opacity-90">Check connection and try again.</p>
          </div>
        </div>
      )}

      {/* Summary cards - Modern grid */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <SummaryCard
          icon={FileArchive}
          label="Total Torrents"
          value={totalTorrents}
          loading={torrentsLoading}
          gradient="from-blue-500/10 to-cyan-500/10"
          iconColor="text-blue-600 dark:text-blue-400"
        />
        <SummaryCard
          icon={Activity}
          label="Verification Progress"
          value={`${avgProgress.toFixed(1)}%`}
          subtitle={`${verifiedPieces.toLocaleString()} / ${totalPieces.toLocaleString()} pieces`}
          loading={statsLoading}
          gradient="from-purple-500/10 to-pink-500/10"
          iconColor="text-purple-600 dark:text-purple-400"
        />
        <SummaryCard
          icon={Users}
          label="Active Seeders"
          value={seedingCount}
          subtitle={`${verifyingCount} verifying`}
          loading={statsLoading}
          gradient="from-green-500/10 to-emerald-500/10"
          iconColor="text-green-600 dark:text-green-400"
        />
        <SummaryCard
          icon={ArrowLeftRight}
          label="Active Transfers"
          value={activeTransfersCount}
          subtitle={`${generatingCount} generating, ${queuedCount} queued`}
          loading={transfersLoading || queueLoading}
          gradient="from-orange-500/10 to-red-500/10"
          iconColor="text-orange-600 dark:text-orange-400"
        />
      </div>

      {/* Verification Progress Section - collapsible */}
      {allServerStats.length > 0 && (
        <Collapsible defaultOpen={false}>
          <div className="rounded-xl border border-border bg-gradient-to-br from-card to-card/50 overflow-hidden shadow-sm">
            <CollapsibleTrigger asChild>
              <button
                type="button"
                className="group flex w-full items-center justify-between gap-4 px-6 py-4 text-left border-b border-border bg-gradient-to-r from-muted/30 to-muted/10 hover:from-muted/50 hover:to-muted/20 transition-colors"
              >
                <div className="flex items-center gap-2">
                  <Activity className="h-5 w-5 text-purple-600 dark:text-purple-400" />
                  <div>
                    <h2 className="text-lg font-semibold text-foreground">Verification status by server</h2>
                    <p className="text-xs text-muted-foreground mt-0.5">Expand to see real-time piece-level progress per server</p>
                  </div>
                </div>
                <div className="flex items-center gap-2 shrink-0">
                  <span className="text-sm text-muted-foreground">
                    {Object.keys(statsByInfoHash).length > 0 ? `${new Set(allServerStats.map(s => s.server_id)).size} servers` : ''}
                  </span>
                  <ChevronRight className="h-5 w-5 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
                </div>
              </button>
            </CollapsibleTrigger>
            <CollapsibleContent>
              <div className="p-6">
                <VerificationOverview stats={allServerStats} serverNameMap={serverNameMap} />
              </div>
            </CollapsibleContent>
          </div>
        </Collapsible>
      )}

      {/* Torrent table */}
      <div className="rounded-xl border border-border bg-card overflow-hidden shadow-sm">
        <div className="px-6 py-4 border-b border-border bg-gradient-to-r from-muted/30 to-muted/10 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
          <div>
            <h2 className="text-lg font-semibold text-foreground flex items-center gap-2">
              <Link2 className="h-5 w-5 text-blue-600 dark:text-blue-400" />
              DCP catalog
            </h2>
            <p className="text-xs text-muted-foreground mt-1">Search by DCP name, expand a row for details · Download .torrent for any DCP</p>
          </div>
          <div className="relative w-full sm:w-72">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
            <Input
              type="search"
              placeholder="Search DCP by name..."
              value={dcpSearch}
              onChange={e => setDcpSearch(e.target.value)}
              className="pl-9 h-9 bg-background"
            />
          </div>
        </div>
        {dcpSearch.trim() && (
          <div className="px-6 py-2 border-b border-border bg-muted/20 text-sm text-muted-foreground">
            {filteredTorrents.length === 0
              ? 'No DCPs match your search'
              : `${filteredTorrents.length} DCP${filteredTorrents.length !== 1 ? 's' : ''} match`}
          </div>
        )}
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/20">
                <th className="w-8 px-2 py-3.5" aria-label="Expand" />
                <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Package</th>
                <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Info Hash</th>
                <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Size</th>
                <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Pieces</th>
                <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Files</th>
                <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Created By</th>
                <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Created</th>
                <th className="px-4 py-3.5 text-center text-xs font-semibold text-muted-foreground uppercase tracking-wider">Seeders</th>
                <th className="px-4 py-3.5 text-center text-xs font-semibold text-muted-foreground uppercase tracking-wider">Transfers</th>
                <th className="px-4 py-3.5 text-left text-xs font-semibold text-muted-foreground uppercase tracking-wider">Server Status</th>
                <th className="px-4 py-3.5 text-right text-xs font-semibold text-muted-foreground uppercase tracking-wider">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {loading && (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    {Array.from({ length: 12 }).map((_, j) => (
                      <td key={j} className="px-4 py-4">
                        <div className="h-4 w-3/4 animate-pulse rounded-md bg-muted" />
                      </td>
                    ))}
                  </tr>
                ))
              )}
              {!loading && torrents.length === 0 && (
                <tr>
                  <td colSpan={12} className="px-4 py-20 text-center text-muted-foreground">
                    <FileArchive className="mx-auto h-12 w-12 mb-4 opacity-20" />
                    <p className="font-semibold text-foreground text-base">No DCPs in catalog</p>
                    <p className="text-xs mt-1.5">DCPs appear here after ingestion</p>
                  </td>
                </tr>
              )}
              {!loading && torrents.length > 0 && filteredTorrents.length === 0 && (
                <tr>
                  <td colSpan={12} className="px-4 py-12 text-center text-muted-foreground">
                    <Search className="mx-auto h-10 w-10 mb-3 opacity-40" />
                    <p className="font-medium text-foreground">No DCPs match your search</p>
                    <p className="text-xs mt-1">Try a different name or content title</p>
                  </td>
                </tr>
              )}
              {!loading && filteredTorrents.map(t => (
                <TorrentRow
                  key={t.id}
                  torrent={t}
                  serverNameMap={serverNameMap}
                  transferCount={(transfersByTorrentId[t.id] ?? []).length}
                  expanded={expandedId === t.id}
                  onToggleExpand={() => setExpandedId(prev => (prev === t.id ? null : t.id))}
                  copied={copied}
                  onCopyHash={copyHash}
                  stats={statsByInfoHash[t.info_hash] ?? []}
                />
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function VerificationOverview({ stats, serverNameMap }: { stats: ServerTorrentStat[]; serverNameMap: Record<string, string> }) {
  // Group by server
  const serverGroups = useMemo(() => {
    const groups: Record<string, ServerTorrentStat[]> = {};
    stats.forEach(stat => {
      if (!groups[stat.server_id]) groups[stat.server_id] = [];
      groups[stat.server_id].push(stat);
    });
    return groups;
  }, [stats]);

  return (
    <div className="space-y-2">
      {Object.entries(serverGroups).map(([serverId, serverStats]) => {
        const serverName = serverNameMap[serverId] || serverId;
        const totalPieces = serverStats.reduce((sum, s) => sum + s.pieces_total, 0);
        const verifiedPieces = serverStats.reduce((sum, s) => sum + s.pieces_completed, 0);
        const progress = totalPieces > 0 ? (verifiedPieces / totalPieces) * 100 : 0;
        const verifying = serverStats.filter(s => s.status === 'verifying').length;
        const seeding = serverStats.filter(s => s.status === 'seeding').length;
        const avgSpeed = serverStats.reduce((sum, s) => sum + (s.download_speed_bps || 0), 0) / serverStats.length;

        return (
          <Collapsible key={serverId} defaultOpen={false}>
            <div className="rounded-lg border border-border bg-card overflow-hidden">
              <CollapsibleTrigger asChild>
                <button
                  type="button"
                  className="group flex w-full items-center justify-between gap-4 px-4 py-3 text-left hover:bg-accent/30 transition-colors"
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-blue-500/20 to-purple-500/20">
                      <ServerIcon className="h-5 w-5 text-blue-600 dark:text-blue-400" />
                    </div>
                    <div className="min-w-0">
                      <h3 className="font-semibold text-foreground truncate">{serverName}</h3>
                      <p className="text-xs text-muted-foreground">
                        {serverStats.length} DCP{serverStats.length !== 1 ? 's' : ''} · {verifying} verifying · {seeding} seeding
                      </p>
                    </div>
                  </div>
                  <div className="flex items-center gap-3 shrink-0">
                    <div className="text-right">
                      <p className="text-xl font-bold text-foreground tabular-nums">{progress.toFixed(1)}%</p>
                      <p className="text-xs text-muted-foreground">{verifiedPieces.toLocaleString()} / {totalPieces.toLocaleString()} pieces</p>
                    </div>
                    <ChevronRight className="h-5 w-5 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
                  </div>
                </button>
              </CollapsibleTrigger>
              <CollapsibleContent>
                <div className="border-t border-border px-4 py-4 space-y-4">
                  {/* Overall progress bar */}
                  <div className="space-y-2">
                    <Progress value={progress} className="h-2" />
                    <div className="flex justify-between text-xs text-muted-foreground">
                      <span>Overall verification progress</span>
                      {avgSpeed > 0 && (
                        <span className="flex items-center gap-1">
                          <TrendingUp className="h-3 w-3" />
                          {formatSpeed(avgSpeed)}
                        </span>
                      )}
                    </div>
                  </div>

                  {/* Individual DCPs */}
                  <div className="grid gap-2">
                    {serverStats
                      .sort((a, b) => b.progress_percent - a.progress_percent)
                      .map(stat => (
                        <TorrentProgressCard key={`${stat.server_id}-${stat.info_hash}`} stat={stat} />
                      ))}
                  </div>
                </div>
              </CollapsibleContent>
            </div>
          </Collapsible>
        );
      })}
    </div>
  );
}

function TorrentProgressCard({ stat }: { stat: ServerTorrentStat }) {
  return (
    <div className="rounded-lg border border-border bg-card p-4 hover:border-border/80 transition-colors">
      <div className="flex items-start justify-between gap-4 mb-3">
        <div className="min-w-0 flex-1">
          <h4 className="font-medium text-foreground truncate">{stat.package_name}</h4>
          {stat.content_title && (
            <p className="text-xs text-muted-foreground truncate mt-0.5">{stat.content_title}</p>
          )}
        </div>
        <div className="shrink-0">
          <Badge className={`${getStatusColor(stat.status)} border text-xs font-medium`}>
            <span className="flex items-center gap-1.5">
              {getStatusIcon(stat.status)}
              {stat.status}
            </span>
          </Badge>
        </div>
      </div>

      {/* Progress bar */}
      <div className="space-y-2">
        <Progress value={stat.progress_percent} className="h-2" />
        <div className="flex items-center justify-between text-xs">
          <span className="text-muted-foreground">
            {stat.pieces_completed.toLocaleString()} / {stat.pieces_total.toLocaleString()} pieces
          </span>
          <span className="font-semibold text-foreground tabular-nums">
            {stat.progress_percent.toFixed(1)}%
          </span>
        </div>
      </div>

      {/* Stats grid */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 mt-4 pt-4 border-t border-border">
        <StatItem
          icon={<HardDrive className="h-3.5 w-3.5" />}
          label="Size"
          value={formatBytes(stat.bytes_total)}
        />
        <StatItem
          icon={<Zap className="h-3.5 w-3.5" />}
          label="Speed"
          value={formatSpeed(stat.download_speed_bps || stat.upload_speed_bps)}
        />
        <StatItem
          icon={<Users className="h-3.5 w-3.5" />}
          label="Peers"
          value={stat.peers_connected.toString()}
        />
        <StatItem
          icon={<Clock className="h-3.5 w-3.5" />}
          label="ETA"
          value={formatETA(stat.eta_seconds)}
        />
      </div>

      {/* Tracker status */}
      {stat.announced_to_tracker && (
        <div className="mt-3 pt-3 border-t border-border flex items-center gap-2 text-xs">
          <CheckCircle2 className="h-3.5 w-3.5 text-green-600 dark:text-green-400" />
          <span className="text-muted-foreground">Announced to tracker</span>
          {stat.last_announce_success && (
            <span className="text-muted-foreground">• {formatRelativeTime(stat.last_announce_success)}</span>
          )}
        </div>
      )}
      {stat.announce_error && (
        <div className="mt-3 pt-3 border-t border-border flex items-start gap-2 text-xs">
          <AlertCircle className="h-3.5 w-3.5 text-red-600 dark:text-red-400 shrink-0 mt-0.5" />
          <span className="text-destructive">{stat.announce_error}</span>
        </div>
      )}
    </div>
  );
}

function StatItem({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) {
  return (
    <div className="flex items-center gap-2">
      <div className="text-muted-foreground">{icon}</div>
      <div>
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className="text-sm font-semibold text-foreground tabular-nums">{value}</p>
      </div>
    </div>
  );
}

function ServerStatusCell({ statuses }: { statuses?: ServerTorrentStatus[] }) {
  if (!statuses?.length) return <span className="text-muted-foreground text-xs">—</span>;
  const errors = statuses.filter(s => s.status === 'error' && s.error_message);
  const seeding = statuses.filter(s => s.status === 'seeding');
  if (errors.length > 0) {
    return (
      <div className="flex flex-col gap-0.5 text-xs">
        <span className="text-destructive font-medium">{errors.length} error(s)</span>
        <span className="text-muted-foreground truncate max-w-[180px]" title={errors[0]?.error_message}>
          {errors[0]?.error_message}
        </span>
      </div>
    );
  }
  if (seeding.length > 0) {
    return <span className="text-xs text-muted-foreground">Seeding</span>;
  }
  return (
    <span className="text-xs text-muted-foreground">
      {statuses.map(s => s.status).join(', ')}
    </span>
  );
}

function SummaryCard({
  icon: Icon,
  label,
  value,
  subtitle,
  loading,
  gradient,
  iconColor,
}: {
  icon: typeof FileArchive;
  label: string;
  value: number | string;
  subtitle?: string;
  loading?: boolean;
  gradient?: string;
  iconColor?: string;
}) {
  return (
    <div className={`rounded-xl border border-border bg-gradient-to-br ${gradient || 'from-card to-card'} p-5 shadow-sm hover:shadow-md transition-shadow`}>
      <div className="flex items-start justify-between gap-3">
        <div className="flex h-11 w-11 items-center justify-center rounded-lg bg-background/50 backdrop-blur-sm">
          {loading ? (
            <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
          ) : (
            <Icon className={`h-5 w-5 ${iconColor || 'text-muted-foreground'}`} />
          )}
        </div>
        <div className="text-right min-w-0 flex-1">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">{label}</p>
          <p className="text-2xl font-bold text-foreground tabular-nums truncate mt-1">
            {loading ? '—' : value}
          </p>
          {subtitle && (
            <p className="text-xs text-muted-foreground mt-1 truncate">{subtitle}</p>
          )}
        </div>
      </div>
    </div>
  );
}

function TorrentRow({
  torrent,
  serverNameMap,
  transferCount,
  expanded,
  onToggleExpand,
  copied,
  onCopyHash,
  stats,
}: {
  torrent: Torrent;
  serverNameMap: Record<string, string>;
  transferCount: number;
  expanded: boolean;
  onToggleExpand: () => void;
  copied: string | null;
  onCopyHash: (hash: string) => void;
  stats: ServerTorrentStat[];
}) {
  const seedersCount = torrent.seeders_count ?? 0;

  const handleDownload = async () => {
    try {
      const res = await torrentsApi.downloadFile(torrent.info_hash);
      const blob = res.data as Blob;
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${torrent.info_hash}.torrent`;
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      // Could toast error
    }
  };

  // Calculate overall progress from stats
  const totalPieces = stats.reduce((sum, s) => sum + s.pieces_total, 0);
  const verifiedPieces = stats.reduce((sum, s) => sum + s.pieces_completed, 0);
  const avgProgress = totalPieces > 0 ? (verifiedPieces / totalPieces) * 100 : 0;

  return (
    <>
      <tr className="hover:bg-accent/30 transition-colors group">
        <td className="w-8 px-2 py-4">
          <button
            type="button"
            onClick={onToggleExpand}
            className="p-1.5 rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            aria-expanded={expanded}
          >
            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          </button>
        </td>
        <td className="px-4 py-4">
          <div className="flex flex-col gap-1">
            <span className="font-semibold text-foreground group-hover:text-primary transition-colors">{torrent.package_name ?? 'Unknown'}</span>
            {torrent.content_title && <span className="text-xs text-muted-foreground truncate max-w-[250px]">{torrent.content_title}</span>}
            {stats.length > 0 && avgProgress < 100 && (
              <div className="flex items-center gap-2 mt-1">
                <Progress value={avgProgress} className="h-1 w-24" />
                <span className="text-xs text-muted-foreground tabular-nums">{avgProgress.toFixed(0)}%</span>
              </div>
            )}
          </div>
        </td>
        <td className="px-4 py-4">
          <div className="flex items-center gap-2 min-w-0 max-w-[140px]">
            <Hash className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
            <code className="text-xs font-mono text-primary truncate" title={torrent.info_hash}>{torrent.info_hash.slice(0, 12)}…</code>
            <button
              type="button"
              onClick={() => onCopyHash(torrent.info_hash)}
              className="text-muted-foreground hover:text-foreground shrink-0 p-1 rounded-md hover:bg-accent transition-colors"
              title="Copy info hash"
            >
              {copied === torrent.info_hash ? <Check className="h-3.5 w-3.5 text-green-600" /> : <Copy className="h-3.5 w-3.5" />}
            </button>
          </div>
        </td>
        <td className="px-4 py-4 text-right font-mono text-sm font-medium">{formatBytes(torrent.total_size_bytes)}</td>
        <td className="px-4 py-4 text-right text-muted-foreground tabular-nums font-medium">{torrent.total_pieces.toLocaleString()}</td>
        <td className="px-4 py-4 text-right text-muted-foreground text-sm">{torrent.file_count}</td>
        <td className="px-4 py-4 text-muted-foreground text-sm">{serverNameMap[torrent.created_by_server_id] ?? '—'}</td>
        <td className="px-4 py-4 text-right text-muted-foreground whitespace-nowrap text-sm">{formatRelativeTime(torrent.created_at)}</td>
        <td className="px-4 py-4 text-center">
          <Badge variant={seedersCount > 0 ? 'default' : 'secondary'} className="font-semibold">
            {seedersCount}
          </Badge>
        </td>
        <td className="px-4 py-4 text-center">
          <Badge variant={transferCount > 0 ? 'default' : 'secondary'} className="font-semibold">
            {transferCount}
          </Badge>
        </td>
        <td className="px-4 py-4 max-w-[200px]">
          <ServerStatusCell statuses={torrent.server_statuses} />
        </td>
        <td className="px-4 py-4 text-right">
          <div className="flex items-center justify-end gap-2">
            <Button variant="default" size="sm" className="h-8 gap-1.5 px-3 text-xs font-medium" onClick={handleDownload} title="Download .torrent file">
              <Download className="h-3.5 w-3.5" />
              Download
            </Button>
            <Button variant="ghost" size="sm" className="h-8 px-2.5 text-xs" onClick={() => onCopyHash(torrent.info_hash)} title="Copy hash">
              {copied === torrent.info_hash ? <Check className="h-3.5 w-3.5 text-green-600" /> : <Copy className="h-3.5 w-3.5" />}
            </Button>
          </div>
        </td>
      </tr>
      {expanded && (
        <tr className="bg-muted/30">
          <td colSpan={12} className="px-4 py-6">
            <ExpandedDetail torrent={torrent} serverNameMap={serverNameMap} stats={stats} />
          </td>
        </tr>
      )}
    </>
  );
}

function ExpandedDetail({ torrent, serverNameMap, stats }: { torrent: Torrent; serverNameMap: Record<string, string>; stats: ServerTorrentStat[] }) {
  const { data: seeders = [], isLoading: seedersLoading } = useQuery({
    queryKey: ['torrent-seeders', torrent.info_hash],
    queryFn: () => torrentsApi.getSeeders(torrent.info_hash),
    enabled: !!torrent.info_hash,
  });

  const { data: transfersList = [], isLoading: transfersLoading } = useQuery({
    queryKey: ['transfers', { torrent_id: torrent.id }],
    queryFn: () => transfersApi.getAll({ torrent_id: torrent.id }),
    enabled: !!torrent.id,
  });

  const { data: announceAttempts = [], isLoading: attemptsLoading } = useQuery({
    queryKey: ['torrent-announce-attempts', torrent.info_hash],
    queryFn: () => torrentsApi.getAnnounceAttempts(torrent.info_hash, 50),
    enabled: !!torrent.info_hash,
    refetchInterval: 5000,
  });

  return (
    <div className="space-y-6">
      {/* Server-level verification status */}
      {stats.length > 0 && (
        <div>
          <h4 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3 flex items-center gap-2">
            <ServerIcon className="h-4 w-4 text-purple-600 dark:text-purple-400" />
            Server Verification Status
          </h4>
          <div className="grid gap-3 sm:grid-cols-2">
            {stats.map((stat, idx) => (
              <div
                key={`${stat.server_id}-${idx}`}
                className="rounded-lg border border-border bg-card p-4 space-y-3"
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 flex-1">
                    <h5 className="font-semibold text-foreground">{stat.server_name}</h5>
                    <p className="text-xs text-muted-foreground mt-0.5">
                      {stat.pieces_completed.toLocaleString()} / {stat.pieces_total.toLocaleString()} pieces
                    </p>
                  </div>
                  <Badge className={`${getStatusColor(stat.status)} border shrink-0`}>
                    <span className="flex items-center gap-1.5">
                      {getStatusIcon(stat.status)}
                      {stat.status}
                    </span>
                  </Badge>
                </div>
                <Progress value={stat.progress_percent} className="h-1.5" />
                <div className="grid grid-cols-3 gap-2 text-xs">
                  <div>
                    <p className="text-muted-foreground">Progress</p>
                    <p className="font-semibold text-foreground tabular-nums">{stat.progress_percent.toFixed(1)}%</p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">Speed</p>
                    <p className="font-semibold text-foreground tabular-nums">{formatSpeed(stat.download_speed_bps || stat.upload_speed_bps)}</p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">ETA</p>
                    <p className="font-semibold text-foreground tabular-nums">{formatETA(stat.eta_seconds)}</p>
                  </div>
                </div>
                {stat.announced_to_tracker && (
                  <div className="flex items-center gap-1.5 text-xs text-green-600 dark:text-green-400 pt-2 border-t border-border">
                    <CheckCircle2 className="h-3 w-3" />
                    <span>Announced to tracker</span>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="grid gap-6 md:grid-cols-2">
        {/* Seeders */}
        <div>
          <h4 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3 flex items-center gap-2">
            <Radio className="h-4 w-4 text-green-600 dark:text-green-400" />
            Seeders
          </h4>
          {seedersLoading && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
              <Loader2 className="h-4 w-4 animate-spin" /> Loading…
            </div>
          )}
          {!seedersLoading && seeders.length === 0 && (
            <p className="text-sm text-muted-foreground py-2 px-4 rounded-lg bg-muted/30 border border-border">No seeders</p>
          )}
          {!seedersLoading && seeders.length > 0 && (
            <ul className="space-y-2">
              {seeders.map((s: Seeder) => {
                const live = isSeederLive(s.last_announce);
                return (
                  <li key={s.id} className="flex items-center justify-between gap-2 text-sm rounded-lg border border-border bg-card px-4 py-3 hover:border-border/80 transition-colors">
                    <span className="font-medium truncate">{serverNameMap[s.server_id] ?? s.server_id}</span>
                    <div className="flex items-center gap-2 shrink-0">
                      <Badge variant={live ? 'default' : 'secondary'} className="text-xs font-semibold">
                        {live ? 'Live' : 'Stale'}
                      </Badge>
                      <span className="text-xs text-muted-foreground whitespace-nowrap" title={s.last_announce}>
                        {formatRelativeTime(s.last_announce)}
                      </span>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </div>

        {/* Transfers */}
        <div>
          <h4 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3 flex items-center gap-2">
            <ArrowLeftRight className="h-4 w-4 text-orange-600 dark:text-orange-400" />
            Transfers
          </h4>
          {transfersLoading && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
              <Loader2 className="h-4 w-4 animate-spin" /> Loading…
            </div>
          )}
          {!transfersLoading && transfersList.length === 0 && (
            <p className="text-sm text-muted-foreground py-2 px-4 rounded-lg bg-muted/30 border border-border">No transfers</p>
          )}
          {!transfersLoading && transfersList.length > 0 && (
            <ul className="space-y-2">
              {transfersList.map((tr: Transfer) => (
                <li key={tr.id} className="flex items-center justify-between gap-2 text-sm rounded-lg border border-border bg-card px-4 py-3 hover:border-border/80 transition-colors">
                  <span className="truncate font-medium">{serverNameMap[tr.destination_server_id] ?? tr.destination_server_id}</span>
                  <div className="flex items-center gap-2 shrink-0">
                    <Badge variant="outline" className="text-xs capitalize font-semibold">{tr.status}</Badge>
                    <span className="text-xs text-muted-foreground tabular-nums font-semibold">{tr.progress_percent.toFixed(0)}%</span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {/* Announce attempts */}
      <div>
        <h4 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3 flex items-center gap-2">
          <SatelliteDish className="h-4 w-4 text-blue-600 dark:text-blue-400" />
          Tracker Announce Attempts
        </h4>
        {attemptsLoading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
            <Loader2 className="h-4 w-4 animate-spin" /> Loading…
          </div>
        )}
        {!attemptsLoading && announceAttempts.length === 0 && (
          <p className="text-sm text-muted-foreground py-2 px-4 rounded-lg bg-muted/30 border border-border">No announce attempts recorded</p>
        )}
        {!attemptsLoading && announceAttempts.length > 0 && (
          <ul className="space-y-2">
            {announceAttempts.map((a: AnnounceAttempt, idx: number) => (
              <li key={`${a.peer_id}-${a.created_at}-${idx}`} className="rounded-lg border border-border bg-card px-4 py-3 text-sm hover:border-border/80 transition-colors">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={a.status === 'ok' ? 'default' : 'destructive'} className="text-xs uppercase font-semibold">
                    {a.status}
                  </Badge>
                  <code className="font-mono text-xs text-muted-foreground bg-muted px-2 py-0.5 rounded">
                    {a.ip}:{a.port}
                  </code>
                  <Badge variant="outline" className="text-xs font-medium">
                    {a.event || 'none'}
                  </Badge>
                  <span className="text-xs text-muted-foreground ml-auto">{formatRelativeTime(a.created_at)}</span>
                </div>
                {a.failure_reason && (
                  <p className="mt-2 text-xs text-destructive break-all bg-destructive/10 px-3 py-2 rounded-md">{a.failure_reason}</p>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
