import { useState } from 'react';
import { useServers } from '@/hooks/useServers';
import { useVersions, useLatestVersion, useTriggerUpgrade, useRestartServer, useCreateBuild } from '@/hooks/useVersions';
import { isServerOnline, formatRelativeTime } from '@/utils/format';
import { OnlineIndicator } from '@/components/common/StatusIndicator';
import { Server, MapPin, HardDrive, Globe, Package, Download, Hammer, Settings } from 'lucide-react';
import { cn } from '@/lib/utils';
import { toast } from 'sonner';
import CreateBuildDialog from '@/components/CreateBuildDialog';
import ServerSettingsDrawer from '@/components/ServerSettingsDrawer';
import PendingServersNotification from '@/components/PendingServersNotification';
import ServerActivityPanel from '@/components/ServerActivityPanel';
import { Button } from '@/components/ui/button';
import { serverDisplayName } from '@/api/servers';
import type { Server as ServerType } from '@/api/types';

interface ServerData {
  id: string;
  name: string;
  location: string;
  mac_address: string;
  api_url: string;
  public_ip: string;
  storage_capacity_tb: number;
  software_version: string;
  is_authorized: boolean;
  last_seen: string;
  upgrade_status?: string;
  upgrade_progress_message?: string;
}

export default function Servers() {
  const { data: serversRaw, isLoading, refetch } = useServers({ pollWhenUpgradePending: true });
  const { data: versions } = useVersions();
  const { data: latestVersion } = useLatestVersion();
  const triggerUpgradeMutation = useTriggerUpgrade();
  const restartServerMutation = useRestartServer();
  const createBuildMutation = useCreateBuild();

  const allServers: ServerData[] = Array.isArray(serversRaw) ? serversRaw : [];

  // Filter servers: authorized servers for main view, unauthorized for notification
  const authorizedServers = allServers.filter(s => s.is_authorized);
  const pendingServers = allServers.filter(s => !s.is_authorized);

  const [createBuildDialogOpen, setCreateBuildDialogOpen] = useState(false);
  const [settingsDrawerOpen, setSettingsDrawerOpen] = useState(false);
  const [selectedServer, setSelectedServer] = useState<ServerData | null>(null);

  const handleOpenSettings = (server: ServerData) => {
    setSelectedServer(server);
    setSettingsDrawerOpen(true);
  };

  const handleRestart = async (serverId: string, serverName: string) => {
    if (!confirm(`Restart ${serverName}? The service will be unavailable briefly.`)) {
      return;
    }

    try {
      await restartServerMutation.mutateAsync(serverId);
      toast.success(`Restart triggered for ${serverName}`);
      setTimeout(() => refetch(), 2000);
    } catch (error) {
      console.error('Restart error:', error);
      toast.error('Failed to trigger restart');
    }
  };

  const handleUpgrade = async (serverId: string, serverName: string, currentVersion: string | null, targetVersion: any) => {
    try {
      await triggerUpgradeMutation.mutateAsync({
        serverId,
        targetVersion: targetVersion.version,
      });
      toast.success(`Upgrade initiated for ${serverName}`);
      setTimeout(() => refetch(), 2000);
    } catch (error) {
      console.error('Upgrade error:', error);
      toast.error('Failed to trigger upgrade');
    }
  };

  const handleCreateBuildSuccess = () => {
    toast.success('Build created and available for client upgrades');
    refetch();
  };

  const isUpdateAvailable = (serverVersion: string | null) => {
    if (!serverVersion || !latestVersion) return false;
    return serverVersion !== latestVersion.version;
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Servers</h1>
          <p className="text-sm text-muted-foreground mt-0.5">{authorizedServers.length} authorized {authorizedServers.length === 1 ? 'site' : 'sites'} in network</p>
        </div>
        <Button
          onClick={() => setCreateBuildDialogOpen(true)}
          disabled={createBuildMutation.isPending}
          className="inline-flex items-center gap-2 bg-primary text-primary-foreground hover:bg-primary/90"
        >
          <Hammer className="h-4 w-4" />
          Create build
        </Button>
      </div>

      {/* Pending Servers Notification */}
      <PendingServersNotification
        pendingServers={pendingServers}
        onRefresh={refetch}
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {isLoading && Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="rounded-xl border border-border bg-card p-5 space-y-3 animate-pulse">
            <div className="h-5 w-1/2 rounded bg-muted" />
            <div className="h-4 w-3/4 rounded bg-muted" />
            <div className="h-4 w-1/3 rounded bg-muted" />
          </div>
        ))}
        {authorizedServers.map(server => {
          const online = isServerOnline(server.last_seen);
          const hasUpdate = isUpdateAvailable(server.software_version);

          return (
            <div
              key={server.id}
              className="group rounded-xl border border-border bg-card hover:border-primary/30 hover:shadow-lg transition-all duration-200"
            >
              <div className="p-6 space-y-4">
                {/* Header */}
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-3">
                    <div className={cn(
                      "flex h-11 w-11 items-center justify-center rounded-xl transition-colors",
                      online ? "bg-success/10 text-success" : "bg-muted text-muted-foreground"
                    )}>
                      <Server className="h-5 w-5" />
                    </div>
                    <div>
                      <h3 className="text-base font-semibold text-foreground leading-tight">{serverDisplayName(server)}</h3>
                      <div className="flex items-center gap-1.5 text-xs text-muted-foreground mt-1">
                        <OnlineIndicator online={online} />
                        <span>{online ? 'Online' : 'Offline'}</span>
                        {!online && (
                          <span className="text-[10px]">• {formatRelativeTime(server.last_seen)}</span>
                        )}
                      </div>
                    </div>
                  </div>
                </div>

                {/* Server Details */}
                <div className="space-y-2 text-sm">
                  <div className="flex items-center gap-2.5 text-muted-foreground">
                    <MapPin className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{server.location || 'No location set'}</span>
                  </div>
                  <div className="flex items-center gap-2.5 text-muted-foreground">
                    <HardDrive className="h-3.5 w-3.5 shrink-0" />
                    <span>{(server.storage_capacity_tb ?? 0).toFixed(1)} TB capacity</span>
                  </div>
                  <div className="flex items-center gap-2.5 text-muted-foreground">
                    <Globe className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate font-mono text-xs" title={server.api_url || ''}>
                      {server.api_url || 'No API URL'}
                    </span>
                  </div>
                </div>

                {/* Version Info */}
                <div className="flex items-center justify-between pt-3 border-t border-border">
                  <div className="flex items-center gap-2">
                    <Package className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm font-medium font-mono">
                      v{server.software_version || 'Unknown'}
                    </span>
                  </div>
                  {hasUpdate && (
                    <span className="inline-flex items-center gap-1 rounded-full px-2 py-1 text-[10px] font-medium bg-info/10 text-info">
                      <Download className="h-2.5 w-2.5" />
                      Update
                    </span>
                  )}
                </div>

                {/* Upgrade Status */}
                {server.upgrade_status && server.upgrade_status !== 'idle' && (
                  <div className={cn(
                    "text-xs font-medium px-3 py-2 rounded-lg",
                    server.upgrade_status === 'upgrading' && "bg-info/10 text-info",
                    server.upgrade_status === 'pending' && "bg-muted text-muted-foreground",
                    server.upgrade_status === 'success' && "bg-success/10 text-success",
                    server.upgrade_status === 'failed' && "bg-destructive/10 text-destructive"
                  )}>
                    {server.upgrade_status === 'upgrading' && '⏳ Upgrading…'}
                    {server.upgrade_status === 'pending' && '⌛ Upgrade Pending'}
                    {server.upgrade_status === 'success' && '✓ Upgrade Complete'}
                    {server.upgrade_status === 'failed' && '✗ Upgrade Failed'}
                  </div>
                )}

                {/* Configure Button */}
                <Button
                  onClick={() => handleOpenSettings(server)}
                  className="w-full mt-2"
                  variant="outline"
                >
                  <Settings className="h-4 w-4 mr-2" />
                  Configure Server
                </Button>
              </div>
            </div>
          );
        })}

        {!isLoading && authorizedServers.length === 0 && (
          <div className="col-span-full rounded-xl border border-border bg-card p-16 text-center">
            <Server className="mx-auto h-10 w-10 text-muted-foreground opacity-30 mb-3" />
            <p className="font-medium text-foreground">No servers registered</p>
            <p className="text-xs text-muted-foreground mt-1">Register your first cinema site to get started</p>
          </div>
        )}
      </div>

      {/* Live Activity Panel */}
      {authorizedServers.length > 0 && (
        <ServerActivityPanel
          serverIds={authorizedServers.map(s => s.id)}
          serverNames={Object.fromEntries((authorizedServers as ServerType[]).map(s => [s.id, serverDisplayName(s)]))}
        />
      )}

      {/* Create Build Dialog */}
      <CreateBuildDialog
        isOpen={createBuildDialogOpen}
        onClose={() => setCreateBuildDialogOpen(false)}
        onSuccess={handleCreateBuildSuccess}
        createBuildMutation={createBuildMutation}
      />

      {/* Settings Drawer */}
      {selectedServer && (
        <ServerSettingsDrawer
          isOpen={settingsDrawerOpen}
          onClose={() => {
            setSettingsDrawerOpen(false);
            setSelectedServer(null);
          }}
          serverId={selectedServer.id}
          serverData={selectedServer}
          onRefresh={refetch}
          versions={versions || []}
          latestVersion={latestVersion}
          onUpgrade={handleUpgrade}
          onRestart={handleRestart}
        />
      )}
    </div>
  );
}
