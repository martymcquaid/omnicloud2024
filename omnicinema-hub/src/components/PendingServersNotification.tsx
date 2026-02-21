import { useState } from 'react';
import { Shield, X, Check, ChevronDown } from 'lucide-react';
import { cn } from '@/lib/utils';
import { Button } from './ui/button';
import { toast } from 'sonner';
import { API_BASE_URL } from '@/api/client';

interface PendingServer {
  id: string;
  name: string;
  display_name?: string;
  location: string;
  mac_address: string;
  api_url: string;
  storage_capacity_tb: number;
}

interface Props {
  pendingServers: PendingServer[];
  onRefresh: () => void;
}

export default function PendingServersNotification({ pendingServers, onRefresh }: Props) {
  const [isExpanded, setIsExpanded] = useState(false);
  const [processing, setProcessing] = useState<string | null>(null);

  if (pendingServers.length === 0) return null;

  const handleAuthorize = async (serverId: string, serverName: string) => {
    setProcessing(serverId);
    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ is_authorized: true }),
      });

      if (!response.ok) {
        throw new Error('Failed to authorize server');
      }

      toast.success(`${serverName} has been authorized`);
      onRefresh();
    } catch (error) {
      console.error('Authorization error:', error);
      toast.error('Failed to authorize server');
    } finally {
      setProcessing(null);
    }
  };

  const handleIgnore = async (serverId: string, serverName: string) => {
    if (!confirm(`Are you sure you want to ignore the registration request from ${serverName}? You can still authorize it later from the Pending Servers section.`)) {
      return;
    }

    setProcessing(serverId);
    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}`, {
        method: 'DELETE',
      });

      if (!response.ok) {
        throw new Error('Failed to delete server');
      }

      toast.success(`Registration request from ${serverName} has been ignored`);
      onRefresh();
    } catch (error) {
      console.error('Delete error:', error);
      toast.error('Failed to ignore server');
    } finally {
      setProcessing(null);
    }
  };

  return (
    <div className="mb-6 rounded-xl border-2 border-warning/50 bg-warning/5 overflow-hidden">
      <button
        onClick={() => setIsExpanded(!isExpanded)}
        className="w-full flex items-center justify-between p-4 hover:bg-warning/10 transition-colors"
      >
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-warning/20 text-warning">
            <Shield className="h-5 w-5" />
          </div>
          <div className="text-left">
            <h3 className="font-semibold text-foreground">
              {pendingServers.length} {pendingServers.length === 1 ? 'Server' : 'Servers'} Pending Authorization
            </h3>
            <p className="text-sm text-muted-foreground">
              {pendingServers.length === 1
                ? 'A new server is waiting for authorization'
                : 'New servers are waiting for authorization'}
            </p>
          </div>
        </div>
        <ChevronDown
          className={cn(
            "h-5 w-5 text-muted-foreground transition-transform",
            isExpanded && "rotate-180"
          )}
        />
      </button>

      {isExpanded && (
        <div className="border-t border-warning/30 p-4 space-y-3 bg-background/50">
          {pendingServers.map((server) => (
            <div
              key={server.id}
              className="flex items-center justify-between p-4 rounded-lg border border-border bg-card"
            >
              <div className="flex-1 min-w-0">
                <h4 className="font-medium text-foreground">{server.display_name?.trim() || server.name}</h4>
                <div className="flex flex-col gap-1 mt-2 text-sm text-muted-foreground">
                  <span>{server.location || 'No location'}</span>
                  <span className="font-mono text-xs">{server.mac_address}</span>
                  <span className="font-mono text-xs truncate">{server.api_url}</span>
                  <span className="text-xs">
                    {(server.storage_capacity_tb ?? 0).toFixed(1)} TB capacity
                  </span>
                </div>
              </div>
              <div className="flex items-center gap-2 ml-4">
                <Button
                  onClick={() => handleAuthorize(server.id, server.display_name?.trim() || server.name)}
                  disabled={processing === server.id}
                  size="sm"
                  className="bg-success text-white hover:bg-success/90"
                >
                  <Check className="h-4 w-4 mr-1" />
                  Authorize
                </Button>
                <Button
                  onClick={() => handleIgnore(server.id, server.display_name?.trim() || server.name)}
                  disabled={processing === server.id}
                  size="sm"
                  variant="outline"
                  className="border-destructive/50 text-destructive hover:bg-destructive/10"
                >
                  <X className="h-4 w-4 mr-1" />
                  Ignore
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
