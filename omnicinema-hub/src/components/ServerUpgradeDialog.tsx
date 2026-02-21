import { useState } from 'react';
import { X, AlertTriangle, Download, Check } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { Version } from '@/hooks/useVersions';

interface ServerUpgradeDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void;
  serverName: string;
  currentVersion: string | null;
  targetVersion: Version | null;
  isLoading: boolean;
}

export default function ServerUpgradeDialog({
  isOpen,
  onClose,
  onConfirm,
  serverName,
  currentVersion,
  targetVersion,
  isLoading,
}: ServerUpgradeDialogProps) {
  if (!isOpen || !targetVersion) return null;

  const formatBytes = (bytes: number) => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`;
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4">
      <div className="bg-white rounded-xl shadow-2xl max-w-2xl w-full max-h-[90vh] overflow-hidden">
        {/* Header */}
        <div className="flex items-center justify-between p-6 border-b border-border">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-warning/10">
              <AlertTriangle className="h-5 w-5 text-warning" />
            </div>
            <div>
              <h2 className="text-lg font-semibold text-foreground">Upgrade Server</h2>
              <p className="text-sm text-muted-foreground">{serverName}</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        {/* Content */}
        <div className="p-6 space-y-6">
          {/* Version Comparison */}
          <div className="grid grid-cols-2 gap-4">
            <div className="bg-muted rounded-lg p-4">
              <div className="text-xs text-muted-foreground mb-1">Current Version</div>
              <div className="text-lg font-semibold text-foreground">
                {currentVersion || 'Unknown'}
              </div>
            </div>
            <div className="bg-primary/10 rounded-lg p-4">
              <div className="text-xs text-primary mb-1">Target Version</div>
              <div className="text-lg font-semibold text-primary">
                {targetVersion.version}
              </div>
            </div>
          </div>

          {/* Package Info */}
          <div className="bg-muted/50 rounded-lg p-4 space-y-2">
            <div className="flex items-center justify-between text-sm">
              <span className="text-muted-foreground">Build Time:</span>
              <span className="font-medium text-foreground">
                {new Date(targetVersion.build_time).toLocaleString()}
              </span>
            </div>
            <div className="flex items-center justify-between text-sm">
              <span className="text-muted-foreground">Package Size:</span>
              <span className="font-medium text-foreground">
                {formatBytes(targetVersion.size_bytes)}
              </span>
            </div>
            {targetVersion.release_notes && (
              <div className="pt-2 mt-2 border-t border-border">
                <div className="text-xs text-muted-foreground mb-1">Release Notes:</div>
                <div className="text-sm text-foreground whitespace-pre-wrap">
                  {targetVersion.release_notes}
                </div>
              </div>
            )}
          </div>

          {/* Warning */}
          <div className="bg-warning/10 border border-warning/20 rounded-lg p-4 flex gap-3">
            <AlertTriangle className="h-5 w-5 text-warning shrink-0 mt-0.5" />
            <div className="space-y-1">
              <div className="text-sm font-medium text-foreground">
                This will restart the OmniCloud service
              </div>
              <div className="text-xs text-muted-foreground">
                The server will be unavailable for a few minutes during the upgrade process.
                The service will automatically restart once the upgrade is complete.
              </div>
            </div>
          </div>
        </div>

        {/* Footer */}
        <div className="flex items-center justify-end gap-3 p-6 border-t border-border bg-muted/30">
          <button
            onClick={onClose}
            disabled={isLoading}
            className="px-4 py-2 text-sm font-medium text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={isLoading}
            className={cn(
              "inline-flex items-center gap-2 px-4 py-2 text-sm font-medium rounded-lg transition-colors",
              "bg-primary text-primary-foreground hover:bg-primary/90",
              "disabled:opacity-50 disabled:cursor-not-allowed"
            )}
          >
            {isLoading ? (
              <>
                <Download className="h-4 w-4 animate-spin" />
                Upgrading...
              </>
            ) : (
              <>
                <Check className="h-4 w-4" />
                Confirm Upgrade
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  );
}
