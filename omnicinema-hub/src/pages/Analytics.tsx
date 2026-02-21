import { useDCPs } from '@/hooks/useDCPs';
import { useServers } from '@/hooks/useServers';
import { useTransfers } from '@/hooks/useTransfers';
import { useMemo } from 'react';
import { PieChart, Pie, Cell, BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts';

const COLORS = [
  'hsl(187, 94%, 43%)',
  'hsl(160, 84%, 39%)',
  'hsl(38, 92%, 50%)',
  'hsl(271, 91%, 65%)',
  'hsl(0, 72%, 51%)',
  'hsl(217, 91%, 60%)',
];

const tooltipStyle = {
  background: 'hsl(217, 33%, 12%)',
  border: '1px solid hsl(217, 33%, 20%)',
  borderRadius: 8,
  color: 'hsl(210, 40%, 96%)',
  fontSize: 12,
};

export default function Analytics() {
  const { data: dcpsRaw } = useDCPs();
  const { data: serversRaw } = useServers();
  const { data: transfersRaw } = useTransfers();

  const dcps = Array.isArray(dcpsRaw) ? dcpsRaw : [];
  const servers = Array.isArray(serversRaw) ? serversRaw : [];
  const transfers = Array.isArray(transfersRaw) ? transfersRaw : [];

  const kindData = useMemo(() => {
    const map: Record<string, number> = {};
    dcps.forEach(d => { map[d.content_kind] = (map[d.content_kind] || 0) + 1; });
    return Object.entries(map).map(([name, value]) => ({ name, value })).sort((a, b) => b.value - a.value);
  }, [dcps]);

  const serverStorageData = useMemo(() =>
    servers.map(s => ({ name: s.name, capacity: s.storage_capacity_tb ?? 0 })).sort((a, b) => b.capacity - a.capacity).slice(0, 10),
    [servers]
  );

  const transferStatusData = useMemo(() => {
    const map: Record<string, number> = {};
    transfers.forEach(t => { map[t.status] = (map[t.status] || 0) + 1; });
    return Object.entries(map).map(([name, value]) => ({ name, value }));
  }, [transfers]);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-foreground">Analytics</h1>
        <p className="text-sm text-muted-foreground mt-0.5">Network statistics and insights</p>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {/* DCPs by Kind */}
        <div className="rounded-xl border border-border bg-card p-5">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-4">DCPs by Content Type</h3>
          {kindData.length > 0 ? (
            <div className="flex items-center gap-6">
              <ResponsiveContainer width="50%" height={200}>
                <PieChart>
                  <Pie data={kindData} dataKey="value" cx="50%" cy="50%" outerRadius={80} strokeWidth={0}>
                    {kindData.map((_, i) => <Cell key={i} fill={COLORS[i % COLORS.length]} />)}
                  </Pie>
                </PieChart>
              </ResponsiveContainer>
              <div className="space-y-2">
                {kindData.map((d, i) => (
                  <div key={d.name} className="flex items-center gap-2 text-sm">
                    <div className="h-2.5 w-2.5 rounded-full shrink-0" style={{ background: COLORS[i % COLORS.length] }} />
                    <span className="text-muted-foreground">{d.name}</span>
                    <span className="font-mono text-foreground tabular-nums">{d.value}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground py-8 text-center">No data</p>
          )}
        </div>

        {/* Transfer Status */}
        <div className="rounded-xl border border-border bg-card p-5">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-4">Transfer Status</h3>
          {transferStatusData.length > 0 ? (
            <ResponsiveContainer width="100%" height={200}>
              <BarChart data={transferStatusData}>
                <XAxis dataKey="name" tick={{ fill: 'hsl(215, 20%, 55%)', fontSize: 11 }} axisLine={false} tickLine={false} />
                <YAxis tick={{ fill: 'hsl(215, 20%, 55%)', fontSize: 11 }} axisLine={false} tickLine={false} />
                <Tooltip contentStyle={tooltipStyle} />
                <Bar dataKey="value" fill="hsl(187, 94%, 43%)" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-sm text-muted-foreground py-8 text-center">No data</p>
          )}
        </div>

        {/* Server Storage */}
        <div className="rounded-xl border border-border bg-card p-5 lg:col-span-2">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground mb-4">Storage Capacity by Server (TB)</h3>
          {serverStorageData.length > 0 ? (
            <ResponsiveContainer width="100%" height={250}>
              <BarChart data={serverStorageData} layout="vertical">
                <XAxis type="number" tick={{ fill: 'hsl(215, 20%, 55%)', fontSize: 11 }} axisLine={false} tickLine={false} />
                <YAxis type="category" dataKey="name" tick={{ fill: 'hsl(215, 20%, 55%)', fontSize: 11 }} axisLine={false} tickLine={false} width={120} />
                <Tooltip contentStyle={tooltipStyle} />
                <Bar dataKey="capacity" fill="hsl(160, 84%, 39%)" radius={[0, 4, 4, 0]} />
              </BarChart>
            </ResponsiveContainer>
          ) : (
            <p className="text-sm text-muted-foreground py-8 text-center">No data</p>
          )}
        </div>
      </div>
    </div>
  );
}
