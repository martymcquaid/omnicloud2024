import { Film, Server, ArrowLeftRight, Download, Activity, HardDrive, Clock, Zap } from 'lucide-react';
import StatsCard from '@/components/Dashboard/StatsCard';
import TransferProgressCard from '@/components/Transfers/TransferProgressCard';
import { useServers } from '@/hooks/useServers';
import { useDCPs } from '@/hooks/useDCPs';
import { useTransfers } from '@/hooks/useTransfers';
import { useTorrents } from '@/hooks/useTorrents';
import { useHealth } from '@/hooks/useHealth';
import { formatBytes, formatRelativeTime } from '@/utils/format';
import { StatusBadge } from '@/components/common/StatusIndicator';
import { serverDisplayName } from '@/api/servers';

export default function Dashboard() {
  const { data: serversRaw } = useServers();
  const { data: dcpsRaw } = useDCPs();
  const { data: transfersRaw } = useTransfers();
  const { data: torrentsRaw } = useTorrents();
  const { data: health } = useHealth();

  const servers = Array.isArray(serversRaw) ? serversRaw : [];
  const dcps = Array.isArray(dcpsRaw) ? dcpsRaw : [];
  const transfers = Array.isArray(transfersRaw) ? transfersRaw : [];
  const torrents = Array.isArray(torrentsRaw) ? torrentsRaw : [];

  const activeTransfers = transfers.filter(t => t.status === 'downloading' || t.status === 'queued');
  const totalStorage = servers.reduce((acc, s) => acc + (s.storage_capacity_tb ?? 0), 0);
  const totalSize = dcps.reduce((acc, d) => acc + (d.total_size_bytes ?? 0), 0);
  const authorizedServers = servers.filter(s => s.is_authorized).length;

  const serverNameMap = servers.reduce((acc, s) => ({ ...acc, [s.id]: serverDisplayName(s) }), {} as Record<string, string>);

  const recentDcps = [...dcps].sort((a, b) => new Date(b.discovered_at).getTime() - new Date(a.discovered_at).getTime()).slice(0, 6);
  const recentTransfers = [...transfers].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()).slice(0, 6);

  return (
    <div className="space-y-8">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Dashboard</h1>
        <p className="text-sm text-muted-foreground mt-0.5">Network overview {health?.version && `· v${health.version}`}</p>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatsCard title="Total DCPs" value={dcps.length} subtitle={formatBytes(totalSize)} icon={Film} variant="primary" />
        <StatsCard title="Sites" value={servers.length} subtitle={`${authorizedServers} authorized`} icon={Server} variant="success" />
        <StatsCard title="Active Transfers" value={activeTransfers.length} icon={Zap} variant="warning" />
        <StatsCard title="Torrents" value={torrents.length} subtitle={`${totalStorage.toFixed(1)} TB capacity`} icon={Download} />
      </div>

      {/* Active transfers */}
      {activeTransfers.length > 0 && (
        <section>
          <div className="flex items-center gap-2 mb-3">
            <Activity className="h-4 w-4 text-primary" />
            <h2 className="text-sm font-semibold text-foreground">Active Transfers</h2>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            {activeTransfers.slice(0, 4).map(t => (
              <TransferProgressCard key={t.id} transfer={t} serverNames={serverNameMap} />
            ))}
          </div>
        </section>
      )}

      {/* Recent activity */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Recent DCPs */}
        <section className="rounded-xl border border-border bg-card">
          <div className="flex items-center gap-2 px-5 py-4 border-b border-border">
            <Film className="h-4 w-4 text-primary" />
            <h3 className="text-sm font-semibold text-foreground">Recently Indexed</h3>
          </div>
          <div className="divide-y divide-border">
            {recentDcps.length === 0 && <p className="text-sm text-muted-foreground px-5 py-8 text-center">No DCPs found</p>}
            {recentDcps.map(d => (
              <div key={d.id} className="flex items-center justify-between px-5 py-3 hover:bg-accent/30 transition-colors">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-foreground truncate">{d.content_title || d.package_name}</p>
                  <p className="text-xs text-muted-foreground mt-0.5">{d.content_kind} · {formatBytes(d.total_size_bytes)}</p>
                </div>
                <div className="flex items-center gap-1 text-xs text-muted-foreground whitespace-nowrap ml-3">
                  <Clock className="h-3 w-3" />
                  <span>{formatRelativeTime(d.discovered_at)}</span>
                </div>
              </div>
            ))}
          </div>
        </section>

        {/* Recent transfers */}
        <section className="rounded-xl border border-border bg-card">
          <div className="flex items-center gap-2 px-5 py-4 border-b border-border">
            <ArrowLeftRight className="h-4 w-4 text-primary" />
            <h3 className="text-sm font-semibold text-foreground">Recent Transfers</h3>
          </div>
          <div className="divide-y divide-border">
            {recentTransfers.length === 0 && <p className="text-sm text-muted-foreground px-5 py-8 text-center">No transfers yet</p>}
            {recentTransfers.map(t => (
              <div key={t.id} className="flex items-center justify-between px-5 py-3 hover:bg-accent/30 transition-colors">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-foreground truncate">{t.package_name || 'Transfer'}</p>
                  <p className="text-xs text-muted-foreground mt-0.5">→ {serverNameMap[t.destination_server_id] || 'Unknown'}</p>
                </div>
                <StatusBadge status={t.status} />
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}
