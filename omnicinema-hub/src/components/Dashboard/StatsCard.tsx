import { cn } from '@/lib/utils';
import type { LucideIcon } from 'lucide-react';

interface StatsCardProps {
  title: string;
  value: string | number;
  subtitle?: string;
  icon: LucideIcon;
  variant?: 'default' | 'primary' | 'success' | 'warning' | 'destructive';
}

const variantBg = {
  default: 'bg-muted/50',
  primary: 'bg-primary/8',
  success: 'bg-success/8',
  warning: 'bg-warning/8',
  destructive: 'bg-destructive/8',
};

const variantText = {
  default: 'text-muted-foreground',
  primary: 'text-primary',
  success: 'text-success',
  warning: 'text-warning',
  destructive: 'text-destructive',
};

export default function StatsCard({ title, value, subtitle, icon: Icon, variant = 'default' }: StatsCardProps) {
  return (
    <div className="rounded-xl border border-border bg-card p-5 transition-colors hover:border-border/80">
      <div className="flex items-center gap-4">
        <div className={cn("flex h-10 w-10 items-center justify-center rounded-lg", variantBg[variant])}>
          <Icon className={cn("h-5 w-5", variantText[variant])} />
        </div>
        <div className="min-w-0">
          <p className="text-xs font-medium text-muted-foreground">{title}</p>
          <p className="text-xl font-semibold text-foreground tabular-nums">{value}</p>
          {subtitle && <p className="text-[11px] text-muted-foreground">{subtitle}</p>}
        </div>
      </div>
    </div>
  );
}
