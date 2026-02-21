import { formatBytes, formatSpeed, formatETA } from '@/utils/format';
import type { Transfer } from '@/api/types';
import { cn } from '@/lib/utils';
import { ArrowRight, X, Users } from 'lucide-react';

interface Props {
  transfer: Transfer;
  serverNames?: Record<string, string>;
  onCancel?: (id: string) => void;
}

export default function TransferProgressCard({ transfer, serverNames = {}, onCancel }: Props) {
  const srcName = transfer.source_server_id ? (serverNames[transfer.source_server_id] || 'Unknown') : 'Swarm';
  const dstName = serverNames[transfer.destination_server_id] || 'Unknown';
  const isActive = transfer.status === 'downloading' || transfer.status === 'queued';

  return (
    <div className="rounded-xl border border-border bg-card p-4 space-y-3">
      <div className="flex items-start justify-between">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold text-foreground truncate">{transfer.package_name || 'DCP Transfer'}</p>
          <div className="flex items-center gap-1 text-xs text-muted-foreground mt-0.5">
            <span>{srcName}</span>
            <ArrowRight className="h-3 w-3 shrink-0" />
            <span>{dstName}</span>
          </div>
        </div>
        <div className="flex items-center gap-2 shrink-0 ml-2">
          <StatusPill status={transfer.status} />
          {isActive && onCancel && (
            <button onClick={() => onCancel(transfer.id)} className="rounded-md p-1 text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors">
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </div>

      {/* Progress */}
      <div className="space-y-1.5">
        <div className="h-1.5 w-full rounded-full bg-muted overflow-hidden">
          <div
            className={cn(
              "h-full rounded-full transition-all duration-500",
              transfer.status === 'completed' ? "bg-success" :
              transfer.status === 'failed' ? "bg-destructive" : "bg-primary"
            )}
            style={{ width: `${transfer.progress_percent}%` }}
          />
        </div>
        <div className="flex items-center justify-between text-[11px] text-muted-foreground">
          <span className="tabular-nums">{transfer.progress_percent.toFixed(1)}%</span>
          <span className="tabular-nums">{formatBytes(transfer.downloaded_bytes)}</span>
        </div>
      </div>

      {/* Stats */}
      {transfer.status === 'downloading' && (
        <div className="flex items-center gap-3 text-[11px] text-muted-foreground pt-0.5">
          <span className="tabular-nums">↓ {formatSpeed(transfer.download_speed_bps)}</span>
          <span className="tabular-nums">↑ {formatSpeed(transfer.upload_speed_bps)}</span>
          <span className="inline-flex items-center gap-0.5"><Users className="h-3 w-3" /> {transfer.peers_connected}</span>
          <span className="ml-auto tabular-nums">ETA {formatETA(transfer.eta_seconds)}</span>
        </div>
      )}
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const styles: Record<string, string> = {
    queued: 'bg-info/10 text-info',
    downloading: 'bg-primary/10 text-primary',
    completed: 'bg-success/10 text-success',
    failed: 'bg-destructive/10 text-destructive',
    cancelled: 'bg-muted text-muted-foreground',
  };
  return (
    <span className={cn("rounded-md px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide", styles[status] || styles.cancelled)}>
      {status}
    </span>
  );
}
