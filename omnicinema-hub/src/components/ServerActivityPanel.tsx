import { useServerActivities } from '@/hooks/useServers';
import type { ActivityItem, ServerActivityResponse } from '@/api/servers';
import { cn } from '@/lib/utils';
import {
  Search, Cog, ArrowDownToLine, ArrowUpFromLine, Clock, AlertCircle,
  Loader2, Activity, Zap, HardDrive
} from 'lucide-react';

interface ServerActivityPanelProps {
  serverIds: string[];
  serverNames: Record<string, string>;
}

function formatETA(seconds: number): string {
  if (seconds <= 0) return '';
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h ${m}m`;
}

function formatTimeAgo(dateStr: string): string {
  const diff = (Date.now() - new Date(dateStr).getTime()) / 1000;
  if (diff < 5) return 'just now';
  if (diff < 60) return `${Math.floor(diff)}s ago`;
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  return `${Math.floor(diff / 3600)}h ago`;
}

function getCategoryIcon(category: string) {
  switch (category) {
    case 'scanner': return <Search className="h-3.5 w-3.5" />;
    case 'torrent_gen': return <Cog className="h-3.5 w-3.5 animate-spin" style={{ animationDuration: '3s' }} />;
    case 'downloading': return <ArrowDownToLine className="h-3.5 w-3.5" />;
    case 'seeding': return <ArrowUpFromLine className="h-3.5 w-3.5" />;
    case 'transfer': return <HardDrive className="h-3.5 w-3.5" />;
    case 'system': return <Activity className="h-3.5 w-3.5" />;
    default: return <Zap className="h-3.5 w-3.5" />;
  }
}

function getCategoryColor(category: string, action: string) {
  if (action === 'error') return 'text-destructive bg-destructive/10';
  switch (category) {
    case 'scanner': return 'text-info bg-info/10';
    case 'torrent_gen': return 'text-warning bg-warning/10';
    case 'downloading': return 'text-primary bg-primary/10';
    case 'seeding': return 'text-success bg-success/10';
    case 'transfer': return 'text-info bg-info/10';
    case 'system': return 'text-muted-foreground bg-muted';
    default: return 'text-muted-foreground bg-muted';
  }
}

function ActivityRow({ item }: { item: ActivityItem }) {
  const colorClasses = getCategoryColor(item.category, item.action);
  const isActive = item.action === 'progress';
  const hasProgress = typeof item.progress === 'number' && item.progress > 0 && item.progress < 100;

  return (
    <div className="flex items-start gap-3 py-2">
      {/* Icon */}
      <div className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-lg mt-0.5", colorClasses)}>
        {getCategoryIcon(item.category)}
      </div>

      {/* Content */}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className={cn("text-sm font-medium", isActive ? "text-foreground" : "text-muted-foreground")}>
            {item.title}
          </span>
          {item.action === 'error' && <AlertCircle className="h-3.5 w-3.5 text-destructive shrink-0" />}
        </div>

        {item.detail && (
          <p className="text-xs text-muted-foreground truncate mt-0.5">{item.detail}</p>
        )}

        {/* Progress bar */}
        {hasProgress && (
          <div className="mt-1.5 flex items-center gap-2">
            <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
              <div
                className={cn(
                  "h-full rounded-full transition-all duration-700 ease-out",
                  item.category === 'downloading' ? "bg-primary" :
                  item.category === 'torrent_gen' ? "bg-warning" : "bg-info"
                )}
                style={{ width: `${Math.min(item.progress!, 100)}%` }}
              />
            </div>
            <span className="text-[10px] font-mono text-muted-foreground w-10 text-right">
              {item.progress!.toFixed(1)}%
            </span>
          </div>
        )}

        {/* Speed + ETA row */}
        {(item.speed || (item.eta_seconds && item.eta_seconds > 0)) && (
          <div className="flex items-center gap-3 mt-1">
            {item.speed && (
              <span className="text-[10px] font-mono text-muted-foreground flex items-center gap-1">
                <Zap className="h-2.5 w-2.5" />
                {item.speed}
              </span>
            )}
            {item.eta_seconds && item.eta_seconds > 0 && (
              <span className="text-[10px] font-mono text-muted-foreground flex items-center gap-1">
                <Clock className="h-2.5 w-2.5" />
                {formatETA(item.eta_seconds)}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function ServerActivityCard({ activity, serverName }: { activity: ServerActivityResponse | null; serverName: string }) {
  const hasActivities = activity && activity.activities && activity.activities.length > 0;
  const isIdle = hasActivities && activity.activities.length === 1 && activity.activities[0].action === 'idle' && activity.activities[0].category === 'system';
  const lastUpdate = activity?.updated_at;

  return (
    <div className="rounded-lg border border-border bg-card/50 p-4">
      {/* Server header */}
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2">
          <div className={cn(
            "h-2 w-2 rounded-full shrink-0",
            hasActivities && !isIdle ? "bg-success animate-pulse" : "bg-muted-foreground"
          )} />
          <h4 className="text-sm font-semibold text-foreground">{serverName}</h4>
        </div>
        {lastUpdate && (
          <span className="text-[10px] text-muted-foreground">{formatTimeAgo(lastUpdate)}</span>
        )}
      </div>

      {/* Activities */}
      {!hasActivities ? (
        <div className="text-xs text-muted-foreground flex items-center gap-2 py-2">
          <Loader2 className="h-3 w-3 animate-spin" />
          Waiting for activity data...
        </div>
      ) : isIdle ? (
        <div className="text-xs text-muted-foreground py-2">
          Server idle - no active operations
        </div>
      ) : (
        <div className="divide-y divide-border/50">
          {activity.activities
            .filter(a => !(a.category === 'system' && a.action === 'idle'))
            .map((item, i) => (
              <ActivityRow key={`${item.category}-${item.action}-${i}`} item={item} />
            ))}
        </div>
      )}
    </div>
  );
}

export default function ServerActivityPanel({ serverIds, serverNames }: ServerActivityPanelProps) {
  const { data } = useServerActivities();

  const activitiesByServer: Record<string, ServerActivityResponse | null> = {};
  for (const id of serverIds) {
    activitiesByServer[id] = null;
  }
  if (data?.servers) {
    for (const sa of data.servers) {
      activitiesByServer[sa.server_id] = sa;
    }
  }

  // Count active servers (have non-idle activities)
  const activeCount = Object.values(activitiesByServer).filter(a =>
    a && a.activities && a.activities.length > 0 &&
    !(a.activities.length === 1 && a.activities[0].action === 'idle' && a.activities[0].category === 'system')
  ).length;

  if (serverIds.length === 0) return null;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Activity className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-sm font-semibold text-foreground">Live Activity</h3>
          {activeCount > 0 && (
            <span className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium bg-success/10 text-success">
              {activeCount} active
            </span>
          )}
        </div>
        <span className="text-[10px] text-muted-foreground">Updates every 5s</span>
      </div>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {serverIds.map(id => (
          <ServerActivityCard
            key={id}
            activity={activitiesByServer[id] ?? null}
            serverName={serverNames[id] || 'Unknown Server'}
          />
        ))}
      </div>
    </div>
  );
}
