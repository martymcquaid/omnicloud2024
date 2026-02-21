import { cn } from '@/lib/utils';

export function StatusBadge({ status }: { status: string }) {
  const styles: Record<string, string> = {
    queued: 'bg-info/10 text-info',
    downloading: 'bg-primary/10 text-primary',
    generating: 'bg-warning/10 text-warning',
    completed: 'bg-success/10 text-success',
    seeding: 'bg-success/10 text-success',
    failed: 'bg-destructive/10 text-destructive',
    error: 'bg-destructive/10 text-destructive',
    cancelled: 'bg-muted text-muted-foreground',
    paused: 'bg-muted text-muted-foreground',
  };
  return (
    <span className={cn("inline-flex rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wider", styles[status] || styles.cancelled)}>
      {status}
    </span>
  );
}

export function OnlineIndicator({ online }: { online: boolean }) {
  return (
    <div className={cn(
      "h-2 w-2 rounded-full shrink-0",
      online ? "bg-success animate-pulse-glow" : "bg-muted-foreground"
    )} />
  );
}
