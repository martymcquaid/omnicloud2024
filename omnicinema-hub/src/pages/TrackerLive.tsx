import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { torrentsApi } from '@/api/torrents';
import type { TrackerSwarmSnapshot, TrackerPeerSnapshot } from '@/api/types';
import { formatBytes, formatRelativeTime } from '@/utils/format';
import { Activity, Radio, RefreshCw, Search, Users } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Badge } from '@/components/ui/badge';
import { Input } from '@/components/ui/input';

export default function TrackerLive() {
  const [query, setQuery] = useState('');

  const { data, isLoading, isError, refetch, isFetching } = useQuery({
    queryKey: ['tracker-live'],
    queryFn: () => torrentsApi.getTrackerLive(),
    refetchInterval: 3000,
  });

  const torrents = data?.torrents ?? [];
  const activeSwarms = data?.active_swarm_peers ?? [];
  const trackerAvailable = data?.tracker_available ?? false;
  const selectedSwarms = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return activeSwarms;
    return activeSwarms.filter(s => s.info_hash.toLowerCase().includes(q));
  }, [activeSwarms, query]);

  const activeTorrents = torrents.filter(t => t.active).length;

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Tracker Live</h1>
          <p className="text-sm text-muted-foreground mt-0.5">Live swarms, peers, and announce health</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={`h-3.5 w-3.5 mr-1.5 ${isFetching ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {isError && (
        <div className="rounded-lg border border-destructive/50 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          Failed to load tracker telemetry.
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <Summary label="Tracker" value={trackerAvailable ? 'Online' : 'Unavailable'} icon={Radio} />
        <Summary label="Known torrents" value={data?.total_torrents ?? 0} icon={Activity} />
        <Summary label="Active torrents" value={activeTorrents} icon={Activity} />
        <Summary label="Active swarms" value={data?.active_swarms ?? 0} icon={Users} />
        <Summary label="Live peers" value={data?.total_live_peers ?? 0} icon={Users} />
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="px-4 py-3 border-b border-border bg-muted/30">
          <h2 className="text-sm font-semibold text-foreground">All Torrents in System</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border">
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground">Package</th>
                <th className="px-4 py-3 text-left text-xs font-medium text-muted-foreground">Info Hash</th>
                <th className="px-4 py-3 text-center text-xs font-medium text-muted-foreground">Live</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Seeders</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Leechers</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Peers</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Attempts (15m)</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Errors (15m)</th>
                <th className="px-4 py-3 text-right text-xs font-medium text-muted-foreground">Last announce</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {isLoading && (
                <tr>
                  <td colSpan={9} className="px-4 py-10 text-center text-muted-foreground">Loading tracker data…</td>
                </tr>
              )}
              {!isLoading && torrents.length === 0 && (
                <tr>
                  <td colSpan={9} className="px-4 py-10 text-center text-muted-foreground">No torrents found</td>
                </tr>
              )}
              {!isLoading && torrents.map(t => (
                <tr key={t.info_hash} className="hover:bg-accent/30">
                  <td className="px-4 py-3">
                    <div className="flex flex-col gap-0.5">
                      <span className="font-medium text-foreground">{t.package_name || 'Unknown'}</span>
                      {t.content_title && <span className="text-xs text-muted-foreground truncate max-w-[340px]">{t.content_title}</span>}
                    </div>
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-muted-foreground">{t.info_hash.slice(0, 16)}…</td>
                  <td className="px-4 py-3 text-center">
                    <Badge variant={t.active ? 'default' : 'secondary'} className="text-xs">
                      {t.active ? 'Active' : 'Idle'}
                    </Badge>
                  </td>
                  <td className="px-4 py-3 text-right tabular-nums">{t.seeders}</td>
                  <td className="px-4 py-3 text-right tabular-nums">{t.leechers}</td>
                  <td className="px-4 py-3 text-right tabular-nums">{t.peers_count}</td>
                  <td className="px-4 py-3 text-right tabular-nums">{t.recent_attempts_15m}</td>
                  <td className="px-4 py-3 text-right tabular-nums">{t.recent_error_attempts_15m}</td>
                  <td className="px-4 py-3 text-right text-xs text-muted-foreground">{formatRelativeTime(t.last_announce || t.last_attempt_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card overflow-hidden">
        <div className="px-4 py-3 border-b border-border bg-muted/30">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <h2 className="text-sm font-semibold text-foreground">Active Swarms and Peers</h2>
            <div className="relative w-full sm:w-72">
              <Search className="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-2.5" />
              <Input
                value={query}
                onChange={e => setQuery(e.target.value)}
                placeholder="Filter by info hash"
                className="h-8 pl-8 text-xs"
              />
            </div>
          </div>
        </div>
        <div className="p-4 space-y-4">
          {selectedSwarms.length === 0 && (
            <div className="text-sm text-muted-foreground py-4">No active swarms</div>
          )}
          {selectedSwarms.map(swarm => (
            <SwarmCard key={swarm.info_hash} swarm={swarm} />
          ))}
        </div>
      </div>
    </div>
  );
}

function Summary({
  label,
  value,
  icon: Icon,
}: {
  label: string;
  value: number | string;
  icon: typeof Activity;
}) {
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-muted">
          <Icon className="h-4.5 w-4.5 text-muted-foreground" />
        </div>
        <div className="min-w-0">
          <p className="text-xs font-medium text-muted-foreground">{label}</p>
          <p className="text-lg font-semibold text-foreground tabular-nums truncate">{value}</p>
        </div>
      </div>
    </div>
  );
}

function SwarmCard({ swarm }: { swarm: TrackerSwarmSnapshot }) {
  return (
    <div className="rounded-lg border border-border">
      <div className="px-3 py-2 border-b border-border bg-muted/20 flex flex-wrap items-center gap-2">
        <code className="text-xs text-primary break-all">{swarm.info_hash}</code>
        <Badge variant="outline" className="text-xs">Seeders {swarm.seeders}</Badge>
        <Badge variant="outline" className="text-xs">Leechers {swarm.leechers}</Badge>
        <Badge variant="outline" className="text-xs">Peers {swarm.peers_count}</Badge>
        <span className="text-xs text-muted-foreground ml-auto">
          Last {formatRelativeTime(swarm.last_announce)}
        </span>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border">
              <th className="px-3 py-2 text-left font-medium text-muted-foreground">Peer</th>
              <th className="px-3 py-2 text-left font-medium text-muted-foreground">IP:Port</th>
              <th className="px-3 py-2 text-right font-medium text-muted-foreground">Role</th>
              <th className="px-3 py-2 text-right font-medium text-muted-foreground">Uploaded</th>
              <th className="px-3 py-2 text-right font-medium text-muted-foreground">Downloaded</th>
              <th className="px-3 py-2 text-right font-medium text-muted-foreground">Left</th>
              <th className="px-3 py-2 text-right font-medium text-muted-foreground">Last seen</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(swarm.peers || []).map((peer: TrackerPeerSnapshot) => (
              <tr key={`${peer.peer_id}-${peer.ip}-${peer.port}`}>
                <td className="px-3 py-2 font-mono text-[11px] text-muted-foreground">{peer.peer_id.slice(0, 20)}</td>
                <td className="px-3 py-2 font-mono">{peer.ip}:{peer.port}</td>
                <td className="px-3 py-2 text-right">
                  <Badge variant={peer.is_seeder ? 'default' : 'secondary'} className="text-[10px]">
                    {peer.is_seeder ? 'Seeder' : 'Leecher'}
                  </Badge>
                </td>
                <td className="px-3 py-2 text-right tabular-nums">{formatBytes(peer.uploaded)}</td>
                <td className="px-3 py-2 text-right tabular-nums">{formatBytes(peer.downloaded)}</td>
                <td className="px-3 py-2 text-right tabular-nums">{formatBytes(peer.left)}</td>
                <td className="px-3 py-2 text-right text-muted-foreground">{formatRelativeTime(peer.last_seen)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
