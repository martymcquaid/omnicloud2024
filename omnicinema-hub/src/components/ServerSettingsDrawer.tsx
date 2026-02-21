import { useState, useEffect } from 'react';
import { X, Save, Plus, Trash2, Server, MapPin, HardDrive, Globe, Shield, ShieldOff, Package, Download, RotateCw, AlertCircle, Check, FolderOpen, Eye, Settings as SettingsIcon, Info, ChevronDown, Link2 } from 'lucide-react';
import { toast } from 'sonner';
import { API_BASE_URL } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { cn } from '@/lib/utils';
import ServerUpgradeDialog from './ServerUpgradeDialog';

interface LibraryLocation {
  id: string;
  name: string;
  path: string;
  is_active: boolean;
  location_type: string;
}

interface ServerSettings {
  server_id: string;
  display_name?: string;
  download_location: string;
  torrent_download_location: string;
  watch_folder: string;
  auto_cleanup_after_ingestion: boolean;
  library_locations: LibraryLocation[];
}

interface ServerData {
  id: string;
  name: string;
  display_name?: string;
  location: string;
  mac_address: string;
  api_url: string;
  public_ip: string;
  storage_capacity_tb: number;
  software_version: string;
  is_authorized: boolean;
  upgrade_status?: string;
  upgrade_progress_message?: string;
}

interface Props {
  isOpen: boolean;
  onClose: () => void;
  serverId: string;
  serverData: ServerData;
  onRefresh: () => void;
  versions?: any[];
  latestVersion?: any;
  onUpgrade: (serverId: string, serverName: string, currentVersion: string | null, targetVersion: any) => void;
  onRestart: (serverId: string, serverName: string) => void;
}

type Section = 'info' | 'libraries' | 'storage' | 'version' | 'danger';

export default function ServerSettingsDrawer({
  isOpen,
  onClose,
  serverId,
  serverData,
  onRefresh,
  versions = [],
  latestVersion,
  onUpgrade,
  onRestart,
}: Props) {
  const [activeSection, setActiveSection] = useState<Section>('info');
  const [settings, setSettings] = useState<ServerSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  // Info section
  const [displayName, setDisplayName] = useState('');
  const [displayLocation, setDisplayLocation] = useState('');

  // Storage section
  const [downloadLocation, setDownloadLocation] = useState('');
  const [torrentDownloadLocation, setTorrentDownloadLocation] = useState('');
  const [watchFolder, setWatchFolder] = useState('');
  const [autoCleanup, setAutoCleanup] = useState(false);

  // Library locations
  const [libraryLocations, setLibraryLocations] = useState<LibraryLocation[]>([]);
  const [newLibraryName, setNewLibraryName] = useState('');
  const [newLibraryPath, setNewLibraryPath] = useState('');

  // Version section
  const [showVersionDropdown, setShowVersionDropdown] = useState(false);
  const [upgradeDialog, setUpgradeDialog] = useState<{
    serverId: string;
    serverName: string;
    currentVersion: string | null;
    targetVersion: any;
  } | null>(null);

  // Fetch settings
  useEffect(() => {
    if (!isOpen || !serverId) return;

    async function fetchSettings() {
      setLoading(true);
      try {
        const response = await fetch(`${API_BASE_URL}/servers/${serverId}/settings`);
        if (response.ok) {
          const data: ServerSettings = await response.json();
          setSettings(data);
          setDownloadLocation(data.download_location || '');
          setTorrentDownloadLocation(data.torrent_download_location || '');
          setWatchFolder(data.watch_folder || '');
          setAutoCleanup(data.auto_cleanup_after_ingestion || false);
          setLibraryLocations(data.library_locations || []);
          setDisplayName((data.display_name ?? serverData.display_name ?? serverData.name) || '');
        } else {
          setDisplayName(serverData.display_name?.trim() || serverData.name || '');
        }
        setDisplayLocation(serverData.location || '');
      } catch (error) {
        console.error('Failed to fetch settings:', error);
        toast.error('Failed to load settings');
      } finally {
        setLoading(false);
      }
    }

    fetchSettings();
  }, [isOpen, serverId, serverData]);

  const handleSaveServerInfo = async () => {
    setSaving(true);
    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          display_name: displayName.trim() || undefined,
          location: displayLocation,
        }),
      });

      if (!response.ok) throw new Error('Failed to save server info');

      toast.success('Server info updated');
      onRefresh();
    } catch (error) {
      console.error('Failed to save server info:', error);
      toast.error('Failed to save server info');
    } finally {
      setSaving(false);
    }
  };

  const handleSaveStorageSettings = async () => {
    setSaving(true);
    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          download_location: downloadLocation,
          torrent_download_location: torrentDownloadLocation,
          watch_folder: watchFolder,
          auto_cleanup_after_ingestion: autoCleanup,
        }),
      });

      if (!response.ok) throw new Error('Failed to save settings');

      toast.success('Storage settings saved');
    } catch (error) {
      console.error('Failed to save settings:', error);
      toast.error('Failed to save settings');
    } finally {
      setSaving(false);
    }
  };

  const handleAddLibraryLocation = async () => {
    if (!newLibraryName || !newLibraryPath) {
      toast.error('Please provide both name and path');
      return;
    }

    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}/library-locations`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newLibraryName,
          path: newLibraryPath,
        }),
      });

      if (!response.ok) throw new Error('Failed to add library location');

      const result = await response.json();
      setLibraryLocations([
        ...libraryLocations,
        {
          id: result.id,
          name: newLibraryName,
          path: newLibraryPath,
          is_active: true,
          location_type: 'standard',
        },
      ]);

      setNewLibraryName('');
      setNewLibraryPath('');
      toast.success('Library location added');
    } catch (error) {
      console.error('Failed to add library location:', error);
      toast.error('Failed to add library location');
    }
  };

  const handleDeleteLibraryLocation = async (locationId: string, name: string) => {
    if (!confirm(`Delete library location "${name}"?`)) return;

    try {
      const response = await fetch(
        `${API_BASE_URL}/servers/${serverId}/library-locations/${locationId}`,
        { method: 'DELETE' }
      );

      if (!response.ok) throw new Error('Failed to delete library location');

      setLibraryLocations(libraryLocations.filter(loc => loc.id !== locationId));
      toast.success('Library location deleted');
    } catch (error) {
      console.error('Failed to delete library location:', error);
      toast.error('Failed to delete library location');
    }
  };

  const handleToggleLibraryLocation = async (location: LibraryLocation) => {
    try {
      const response = await fetch(
        `${API_BASE_URL}/servers/${serverId}/library-locations/${location.id}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: location.name,
            path: location.path,
            is_active: !location.is_active,
          }),
        }
      );

      if (!response.ok) throw new Error('Failed to update library location');

      setLibraryLocations(
        libraryLocations.map(loc =>
          loc.id === location.id ? { ...loc, is_active: !loc.is_active } : loc
        )
      );

      toast.success(
        !location.is_active ? 'Library location enabled' : 'Library location disabled'
      );
    } catch (error) {
      console.error('Failed to toggle library location:', error);
      toast.error('Failed to update library location');
    }
  };

  const handleToggleRosettaBridge = async (location: LibraryLocation) => {
    const isCurrentlyRB = location.location_type === 'rosettabridge';
    const newType = isCurrentlyRB ? 'standard' : 'rosettabridge';

    try {
      const response = await fetch(
        `${API_BASE_URL}/servers/${serverId}/library-locations/${location.id}`,
        {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: location.name,
            path: location.path,
            is_active: location.is_active,
            location_type: newType,
          }),
        }
      );

      if (!response.ok) throw new Error('Failed to update library location');

      // If setting as rosettabridge, clear any other rosettabridge locations
      setLibraryLocations(
        libraryLocations.map(loc => {
          if (loc.id === location.id) {
            return { ...loc, location_type: newType };
          }
          // Clear other rosettabridge locations when setting a new one
          if (!isCurrentlyRB && loc.location_type === 'rosettabridge') {
            return { ...loc, location_type: 'standard' };
          }
          return loc;
        })
      );

      toast.success(
        isCurrentlyRB
          ? `Removed RosettaBridge designation from ${location.name}`
          : `${location.name} set as RosettaBridge location`
      );
    } catch (error) {
      console.error('Failed to toggle RosettaBridge:', error);
      toast.error('Failed to update RosettaBridge setting');
    }
  };

  const handleRevokeAccess = async () => {
    if (!confirm(`${serverData.is_authorized ? 'Revoke' : 'Grant'} authorization for ${serverData.name}?`)) return;

    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ is_authorized: !serverData.is_authorized }),
      });

      if (!response.ok) throw new Error('Failed to update authorization');

      toast.success(
        !serverData.is_authorized
          ? `${serverData.name} has been authorized`
          : `${serverData.name} has been revoked`
      );
      onRefresh();
    } catch (error) {
      console.error('Authorization error:', error);
      toast.error('Failed to update authorization');
    }
  };

  const handleDeleteServer = async () => {
    if (!confirm(`Are you sure you want to delete ${serverData.name}? This will remove all inventory entries for this server.`)) return;

    try {
      const response = await fetch(`${API_BASE_URL}/servers/${serverId}`, {
        method: 'DELETE',
      });

      if (!response.ok) throw new Error('Failed to delete server');

      toast.success(`${serverData.name} has been deleted`);
      onClose();
      onRefresh();
    } catch (error) {
      console.error('Delete error:', error);
      toast.error('Failed to delete server');
    }
  };

  const handleUpgradeClick = (targetVersion: any) => {
    setUpgradeDialog({
      serverId,
      serverName: serverData.display_name?.trim() || serverData.name,
      currentVersion: serverData.software_version,
      targetVersion,
    });
    setShowVersionDropdown(false);
  };

  const handleUpgradeConfirm = async () => {
    if (!upgradeDialog) return;

    try {
      onUpgrade(
        upgradeDialog.serverId,
        upgradeDialog.serverName,
        upgradeDialog.currentVersion,
        upgradeDialog.targetVersion
      );
      setUpgradeDialog(null);
      onClose();
    } catch (error) {
      console.error('Upgrade error:', error);
    }
  };

  if (!isOpen) return null;

  const menuItems: { section: Section; icon: any; label: string }[] = [
    { section: 'info', icon: Info, label: 'Server Info' },
    { section: 'libraries', icon: FolderOpen, label: 'Library Locations' },
    { section: 'storage', icon: HardDrive, label: 'Storage Settings' },
    { section: 'version', icon: Package, label: 'Version Control' },
    { section: 'danger', icon: AlertCircle, label: 'Danger Zone' },
  ];

  return (
    <>
      {/* Overlay */}
      <div
        className="fixed inset-0 bg-black/50 z-40"
        onClick={onClose}
      />

      {/* Drawer */}
      <div className="fixed right-0 top-0 bottom-0 w-full max-w-4xl bg-background z-50 shadow-2xl flex">
        {/* Left Sidebar Menu */}
        <div className="w-56 bg-muted/30 border-r border-border p-4">
          <div className="flex items-center gap-2 mb-6">
            <Server className="h-5 w-5 text-primary" />
            <h2 className="font-semibold text-sm">Server Settings</h2>
          </div>

          <nav className="space-y-1">
            {menuItems.map(({ section, icon: Icon, label }) => (
              <button
                key={section}
                onClick={() => setActiveSection(section)}
                className={cn(
                  "w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors",
                  activeSection === section
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:bg-muted hover:text-foreground"
                )}
              >
                <Icon className="h-4 w-4" />
                {label}
              </button>
            ))}
          </nav>
        </div>

        {/* Main Content Area */}
        <div className="flex-1 flex flex-col">
          {/* Header */}
          <div className="flex items-center justify-between border-b border-border p-6">
            <div>
              <h2 className="text-xl font-semibold">{serverData.name}</h2>
              <p className="text-sm text-muted-foreground mt-0.5">
                {serverData.location || 'Unknown Location'}
              </p>
            </div>
            <Button variant="ghost" size="sm" onClick={onClose}>
              <X className="h-5 w-5" />
            </Button>
          </div>

          {/* Content */}
          <div className="flex-1 overflow-y-auto p-6">
            {loading ? (
              <div className="flex items-center justify-center h-full">
                <div className="text-center">
                  <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary mx-auto mb-4"></div>
                  <p className="text-muted-foreground">Loading settings...</p>
                </div>
              </div>
            ) : (
              <div className="space-y-6">
                {/* Server Info Section */}
                {activeSection === 'info' && (
                  <div className="space-y-6">
                    <div>
                      <h3 className="text-lg font-semibold mb-4">Server Information</h3>

                      <div className="space-y-4">
                        <div>
                          <Label htmlFor="display-name">Display Name</Label>
                          <Input
                            id="display-name"
                            value={displayName}
                            onChange={(e) => setDisplayName(e.target.value)}
                            placeholder="e.g., Cinema 1 - Main Server"
                            className="mt-1.5"
                          />
                        </div>

                        <div>
                          <Label htmlFor="display-location">Location</Label>
                          <Input
                            id="display-location"
                            value={displayLocation}
                            onChange={(e) => setDisplayLocation(e.target.value)}
                            placeholder="e.g., Downtown Cinema, Screen 5"
                            className="mt-1.5"
                          />
                        </div>

                        <div className="flex justify-end pt-2">
                          <Button onClick={handleSaveServerInfo} disabled={saving}>
                            <Save className="h-4 w-4 mr-2" />
                            {saving ? 'Saving...' : 'Save Changes'}
                          </Button>
                        </div>
                      </div>
                    </div>

                    <div className="border-t pt-6">
                      <h3 className="text-sm font-semibold mb-4 text-muted-foreground">System Information</h3>
                      <div className="grid grid-cols-2 gap-4">
                        <div className="space-y-1">
                          <p className="text-xs text-muted-foreground">Storage Capacity</p>
                          <p className="text-sm font-medium flex items-center gap-2">
                            <HardDrive className="h-4 w-4" />
                            {(serverData.storage_capacity_tb ?? 0).toFixed(2)} TB
                          </p>
                        </div>

                        <div className="space-y-1">
                          <p className="text-xs text-muted-foreground">Public IP</p>
                          <p className="text-sm font-medium flex items-center gap-2">
                            <Globe className="h-4 w-4" />
                            {serverData.public_ip || 'N/A'}
                          </p>
                        </div>

                        <div className="space-y-1">
                          <p className="text-xs text-muted-foreground">MAC Address</p>
                          <p className="text-sm font-mono">{serverData.mac_address || 'N/A'}</p>
                        </div>

                        <div className="space-y-1">
                          <p className="text-xs text-muted-foreground">API URL</p>
                          <p className="text-sm font-mono truncate">{serverData.api_url || 'N/A'}</p>
                        </div>

                        <div className="space-y-1">
                          <p className="text-xs text-muted-foreground">Authorization Status</p>
                          <p className={cn(
                            "text-sm font-medium inline-flex items-center gap-1",
                            serverData.is_authorized ? "text-success" : "text-warning"
                          )}>
                            {serverData.is_authorized ? (
                              <><Shield className="h-4 w-4" /> Authorized</>
                            ) : (
                              <><ShieldOff className="h-4 w-4" /> Pending</>
                            )}
                          </p>
                        </div>

                        <div className="space-y-1">
                          <p className="text-xs text-muted-foreground">Software Version</p>
                          <p className="text-sm font-mono">v{serverData.software_version || 'Unknown'}</p>
                        </div>
                      </div>
                    </div>
                  </div>
                )}

                {/* Library Locations Section */}
                {activeSection === 'libraries' && (
                  <div className="space-y-6">
                    <div>
                      <h3 className="text-lg font-semibold mb-2">Library Locations</h3>
                      <p className="text-sm text-muted-foreground mb-4">
                        Define multiple library paths where DCPs are stored and scanned
                      </p>
                    </div>

                    {libraryLocations.length > 0 ? (
                      <div className="space-y-2">
                        {libraryLocations.map((location) => (
                          <div
                            key={location.id}
                            className={cn(
                              "flex items-center gap-3 p-4 rounded-lg border",
                              location.is_active
                                ? "bg-card border-border"
                                : "bg-muted/50 border-muted"
                            )}
                          >
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <h4 className="font-medium">{location.name}</h4>
                                <span
                                  className={cn(
                                    "text-xs px-2 py-0.5 rounded-full",
                                    location.is_active
                                      ? "bg-success/10 text-success"
                                      : "bg-muted text-muted-foreground"
                                  )}
                                >
                                  {location.is_active ? 'Active' : 'Inactive'}
                                </span>
                                {location.location_type === 'rosettabridge' && (
                                  <span className="text-xs px-2 py-0.5 rounded-full bg-purple-500/10 text-purple-500">
                                    RosettaBridge
                                  </span>
                                )}
                              </div>
                              <p className="text-sm text-muted-foreground font-mono mt-1 truncate">
                                {location.path}
                              </p>
                            </div>
                            <div className="flex items-center gap-2">
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleToggleRosettaBridge(location)}
                                title={location.location_type === 'rosettabridge' ? 'Remove RosettaBridge designation' : 'Set as RosettaBridge location'}
                                className={cn(
                                  location.location_type === 'rosettabridge' && "text-purple-500 hover:text-purple-600"
                                )}
                              >
                                <Link2 className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleToggleLibraryLocation(location)}
                              >
                                <Eye className="h-4 w-4" />
                              </Button>
                              <Button
                                variant="ghost"
                                size="sm"
                                onClick={() => handleDeleteLibraryLocation(location.id, location.name)}
                                className="text-destructive hover:text-destructive"
                              >
                                <Trash2 className="h-4 w-4" />
                              </Button>
                            </div>
                          </div>
                        ))}
                      </div>
                    ) : (
                      <Alert>
                        <AlertCircle className="h-4 w-4" />
                        <AlertDescription>
                          No library locations configured. Add at least one location to begin scanning for DCPs.
                        </AlertDescription>
                      </Alert>
                    )}

                    <div className="border-t pt-6">
                      <h4 className="font-medium mb-4">Add New Library Location</h4>
                      <div className="space-y-4">
                        <div>
                          <Label htmlFor="new-library-name">Library Name</Label>
                          <Input
                            id="new-library-name"
                            value={newLibraryName}
                            onChange={(e) => setNewLibraryName(e.target.value)}
                            placeholder="e.g., Main Library, Archive, Vault A"
                            className="mt-1.5"
                          />
                        </div>
                        <div>
                          <Label htmlFor="new-library-path">Library Path</Label>
                          <Input
                            id="new-library-path"
                            value={newLibraryPath}
                            onChange={(e) => setNewLibraryPath(e.target.value)}
                            placeholder="/mnt/storage/dcp-library"
                            className="font-mono mt-1.5"
                          />
                        </div>
                        <div className="flex justify-end">
                          <Button
                            onClick={handleAddLibraryLocation}
                            disabled={!newLibraryName || !newLibraryPath}
                          >
                            <Plus className="h-4 w-4 mr-2" />
                            Add Library Location
                          </Button>
                        </div>
                      </div>
                    </div>
                  </div>
                )}

                {/* Storage Settings Section */}
                {activeSection === 'storage' && (
                  <div className="space-y-6">
                    <div>
                      <h3 className="text-lg font-semibold mb-2">Storage Settings</h3>
                      <p className="text-sm text-muted-foreground mb-4">
                        Configure download and watch folder locations
                      </p>
                    </div>

                    <div className="space-y-4">
                      <div>
                        <Label htmlFor="general-download-location">General Download Location</Label>
                        <Input
                          id="general-download-location"
                          value={downloadLocation}
                          onChange={(e) => setDownloadLocation(e.target.value)}
                          placeholder="/var/omnicloud/downloads"
                          className="font-mono mt-1.5"
                        />
                        <p className="text-xs text-muted-foreground mt-1.5">
                          General download/staging directory for DCP content and transfers
                        </p>
                      </div>

                      <div>
                        <Label htmlFor="torrent-download-location">Torrent Download Location</Label>
                        <Input
                          id="torrent-download-location"
                          value={torrentDownloadLocation}
                          onChange={(e) => setTorrentDownloadLocation(e.target.value)}
                          placeholder="/mnt/torrents/data"
                          className="font-mono mt-1.5"
                        />
                        <p className="text-xs text-muted-foreground mt-1.5">
                          Directory where BitTorrent client downloads and seeds content (must be separate from library locations)
                        </p>
                      </div>

                      <div>
                        <Label htmlFor="watch-folder">Watch Folder</Label>
                        <Input
                          id="watch-folder"
                          value={watchFolder}
                          onChange={(e) => setWatchFolder(e.target.value)}
                          placeholder="/var/omnicloud/watch"
                          className="font-mono mt-1.5"
                        />
                        <p className="text-xs text-muted-foreground mt-1.5">
                          Monitor this folder for new DCPs to automatically ingest and process
                        </p>
                      </div>

                      <div className="border-t pt-4">
                        <div className="flex items-center justify-between">
                          <div>
                            <Label>Auto-Cleanup After RosettaBridge Ingestion</Label>
                            <p className="text-xs text-muted-foreground mt-1">
                              Automatically remove the original download copy after RosettaBridge has ingested the DCP
                            </p>
                          </div>
                          <button
                            type="button"
                            role="switch"
                            aria-checked={autoCleanup}
                            onClick={() => setAutoCleanup(!autoCleanup)}
                            className={cn(
                              "relative inline-flex h-6 w-11 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors",
                              autoCleanup ? "bg-purple-500" : "bg-muted"
                            )}
                          >
                            <span
                              className={cn(
                                "pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow-lg ring-0 transition-transform",
                                autoCleanup ? "translate-x-5" : "translate-x-0"
                              )}
                            />
                          </button>
                        </div>
                      </div>

                      <div className="flex justify-end pt-2">
                        <Button onClick={handleSaveStorageSettings} disabled={saving}>
                          <Save className="h-4 w-4 mr-2" />
                          {saving ? 'Saving...' : 'Save Settings'}
                        </Button>
                      </div>
                    </div>
                  </div>
                )}

                {/* Version Control Section */}
                {activeSection === 'version' && (
                  <div className="space-y-6">
                    <div>
                      <h3 className="text-lg font-semibold mb-2">Version Control</h3>
                      <p className="text-sm text-muted-foreground mb-4">
                        Manage software version and upgrades
                      </p>
                    </div>

                    <div className="space-y-4">
                      <div className="p-4 rounded-lg border bg-muted/30">
                        <div className="flex items-center justify-between mb-3">
                          <div>
                            <p className="text-sm text-muted-foreground">Current Version</p>
                            <p className="text-lg font-mono font-semibold">
                              v{serverData.software_version || 'Unknown'}
                            </p>
                          </div>
                          {latestVersion && serverData.software_version !== latestVersion.version && (
                            <span className="inline-flex items-center gap-1 rounded-full px-3 py-1 text-xs font-medium bg-info/10 text-info">
                              <Download className="h-3 w-3" />
                              Update Available
                            </span>
                          )}
                        </div>

                        {serverData.upgrade_status && serverData.upgrade_status !== 'idle' && (
                          <div className="mb-3">
                            <div className={cn(
                              "text-xs font-medium px-3 py-1.5 rounded",
                              serverData.upgrade_status === 'upgrading' && "bg-info/10 text-info",
                              serverData.upgrade_status === 'pending' && "bg-muted text-muted-foreground",
                              serverData.upgrade_status === 'success' && "bg-success/10 text-success",
                              serverData.upgrade_status === 'failed' && "bg-destructive/10 text-destructive"
                            )}>
                              {serverData.upgrade_status === 'upgrading' && '⏳ Upgrading…'}
                              {serverData.upgrade_status === 'pending' && '⌛ Upgrade Pending'}
                              {serverData.upgrade_status === 'success' && '✓ Upgrade Complete'}
                              {serverData.upgrade_status === 'failed' && '✗ Upgrade Failed'}
                            </div>
                            {serverData.upgrade_progress_message && (
                              <p className="text-xs text-muted-foreground mt-2">
                                {serverData.upgrade_progress_message}
                              </p>
                            )}
                          </div>
                        )}
                      </div>

                      <div className="flex gap-2">
                        <div className="relative flex-1">
                          <Button
                            onClick={() => setShowVersionDropdown(!showVersionDropdown)}
                            className="w-full"
                            variant="default"
                          >
                            <Download className="h-4 w-4 mr-2" />
                            Upgrade to Version
                            <ChevronDown className="h-4 w-4 ml-2" />
                          </Button>

                          {showVersionDropdown && (
                            <div className="absolute left-0 top-full mt-2 z-10 bg-background rounded-lg shadow-lg border border-border w-full max-h-64 overflow-y-auto">
                              {versions && versions.length > 0 ? (
                                versions.map((version) => (
                                  <button
                                    key={version.version}
                                    onClick={() => handleUpgradeClick(version)}
                                    className={cn(
                                      "w-full text-left px-4 py-3 hover:bg-muted transition-colors border-b border-border last:border-0",
                                      version.version === serverData.software_version && "bg-primary/5 text-primary font-medium"
                                    )}
                                  >
                                    <div className="flex items-center justify-between">
                                      <span className="font-mono text-sm">{version.version}</span>
                                      {version.version === latestVersion?.version && (
                                        <span className="text-xs bg-success/10 text-success px-2 py-0.5 rounded">Latest</span>
                                      )}
                                    </div>
                                    <div className="text-xs text-muted-foreground mt-1">
                                      {new Date(version.build_time).toLocaleDateString()}
                                    </div>
                                  </button>
                                ))
                              ) : (
                                <div className="px-4 py-3 text-sm text-muted-foreground">
                                  No versions available
                                </div>
                              )}
                            </div>
                          )}
                        </div>

                        <Button
                          onClick={() => onRestart(serverId, serverData.name)}
                          variant="outline"
                        >
                          <RotateCw className="h-4 w-4 mr-2" />
                          Restart
                        </Button>
                      </div>
                    </div>
                  </div>
                )}

                {/* Danger Zone Section */}
                {activeSection === 'danger' && (
                  <div className="space-y-6">
                    <div>
                      <h3 className="text-lg font-semibold mb-2 text-destructive">Danger Zone</h3>
                      <p className="text-sm text-muted-foreground mb-4">
                        Irreversible actions that affect server access and data
                      </p>
                    </div>

                    <div className="space-y-4">
                      <div className="p-4 rounded-lg border border-warning/50 bg-warning/5">
                        <div className="flex items-start justify-between">
                          <div className="flex-1">
                            <h4 className="font-medium mb-1">
                              {serverData.is_authorized ? 'Revoke Authorization' : 'Grant Authorization'}
                            </h4>
                            <p className="text-sm text-muted-foreground">
                              {serverData.is_authorized
                                ? 'This server will no longer be able to access the network'
                                : 'Grant this server access to the network'}
                            </p>
                          </div>
                          <Button
                            onClick={handleRevokeAccess}
                            variant={serverData.is_authorized ? "outline" : "default"}
                            className={serverData.is_authorized ? "border-warning text-warning hover:bg-warning/10" : ""}
                          >
                            {serverData.is_authorized ? (
                              <><ShieldOff className="h-4 w-4 mr-2" /> Revoke Access</>
                            ) : (
                              <><Check className="h-4 w-4 mr-2" /> Grant Access</>
                            )}
                          </Button>
                        </div>
                      </div>

                      <div className="p-4 rounded-lg border border-destructive/50 bg-destructive/5">
                        <div className="flex items-start justify-between">
                          <div className="flex-1">
                            <h4 className="font-medium mb-1 text-destructive">Delete Server</h4>
                            <p className="text-sm text-muted-foreground">
                              Permanently remove this server and all its inventory entries. This action cannot be undone.
                            </p>
                          </div>
                          <Button
                            onClick={handleDeleteServer}
                            variant="destructive"
                          >
                            <Trash2 className="h-4 w-4 mr-2" />
                            Delete Server
                          </Button>
                        </div>
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* Upgrade Dialog */}
      <ServerUpgradeDialog
        isOpen={!!upgradeDialog}
        onClose={() => setUpgradeDialog(null)}
        onConfirm={handleUpgradeConfirm}
        serverName={upgradeDialog?.serverName || ''}
        currentVersion={upgradeDialog?.currentVersion || null}
        targetVersion={upgradeDialog?.targetVersion || null}
        isLoading={false}
      />
    </>
  );
}
