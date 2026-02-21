import { useState, useEffect } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ChevronLeft, Save, Plus, Trash2, FolderOpen, Download, Eye, AlertCircle } from 'lucide-react';
import { toast } from 'sonner';
import { API_BASE_URL } from '@/api/client';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { cn } from '@/lib/utils';

interface LibraryLocation {
  id: string;
  name: string;
  path: string;
  is_active: boolean;
}

interface ServerSettings {
  server_id: string;
  download_location: string;
  watch_folder: string;
  library_locations: LibraryLocation[];
}

interface Server {
  id: string;
  name: string;
  display_name?: string;
  location: string;
}

export default function ServerSettings() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [server, setServer] = useState<Server | null>(null);
  const [settings, setSettings] = useState<ServerSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const [downloadLocation, setDownloadLocation] = useState('');
  const [watchFolder, setWatchFolder] = useState('');
  const [libraryLocations, setLibraryLocations] = useState<LibraryLocation[]>([]);
  const [newLibraryName, setNewLibraryName] = useState('');
  const [newLibraryPath, setNewLibraryPath] = useState('');

  // Fetch server details and settings
  useEffect(() => {
    async function fetchData() {
      if (!id) return;

      try {
        // Fetch server info
        const serverResponse = await fetch(`${API_BASE_URL}/servers`);
        const serverData = await serverResponse.json();
        const currentServer = serverData.servers?.find((s: Server) => s.id === id);
        if (currentServer) {
          setServer(currentServer);
        }

        // Fetch server settings
        const settingsResponse = await fetch(`${API_BASE_URL}/servers/${id}/settings`);
        if (settingsResponse.ok) {
          const settingsData: ServerSettings = await settingsResponse.json();
          setSettings(settingsData);
          setDownloadLocation(settingsData.download_location || '');
          setWatchFolder(settingsData.watch_folder || '');
          setLibraryLocations(settingsData.library_locations || []);
        }
      } catch (error) {
        console.error('Failed to fetch server settings:', error);
        toast.error('Failed to load server settings');
      } finally {
        setLoading(false);
      }
    }

    fetchData();
  }, [id]);

  const handleSaveBasicSettings = async () => {
    if (!id) return;

    setSaving(true);
    try {
      const response = await fetch(`${API_BASE_URL}/servers/${id}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          download_location: downloadLocation,
          watch_folder: watchFolder,
        }),
      });

      if (!response.ok) {
        throw new Error('Failed to save settings');
      }

      toast.success('Settings saved successfully');
    } catch (error) {
      console.error('Failed to save settings:', error);
      toast.error('Failed to save settings');
    } finally {
      setSaving(false);
    }
  };

  const handleAddLibraryLocation = async () => {
    if (!id || !newLibraryName || !newLibraryPath) {
      toast.error('Please provide both name and path');
      return;
    }

    try {
      const response = await fetch(`${API_BASE_URL}/servers/${id}/library-locations`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: newLibraryName,
          path: newLibraryPath,
        }),
      });

      if (!response.ok) {
        throw new Error('Failed to add library location');
      }

      const result = await response.json();

      // Add to local state
      setLibraryLocations([
        ...libraryLocations,
        {
          id: result.id,
          name: newLibraryName,
          path: newLibraryPath,
          is_active: true,
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
    if (!id) return;

    if (!confirm(`Delete library location "${name}"?`)) {
      return;
    }

    try {
      const response = await fetch(
        `${API_BASE_URL}/servers/${id}/library-locations/${locationId}`,
        { method: 'DELETE' }
      );

      if (!response.ok) {
        throw new Error('Failed to delete library location');
      }

      setLibraryLocations(libraryLocations.filter(loc => loc.id !== locationId));
      toast.success('Library location deleted');
    } catch (error) {
      console.error('Failed to delete library location:', error);
      toast.error('Failed to delete library location');
    }
  };

  const handleToggleLibraryLocation = async (location: LibraryLocation) => {
    if (!id) return;

    try {
      const response = await fetch(
        `${API_BASE_URL}/servers/${id}/library-locations/${location.id}`,
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

      if (!response.ok) {
        throw new Error('Failed to update library location');
      }

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

  if (loading) {
    return (
      <div className="flex items-center justify-center h-screen">
        <div className="text-center">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-primary mx-auto mb-4"></div>
          <p className="text-muted-foreground">Loading settings...</p>
        </div>
      </div>
    );
  }

  if (!server) {
    return (
      <div className="flex items-center justify-center h-screen">
        <Alert variant="destructive">
          <AlertCircle className="h-4 w-4" />
          <AlertDescription>Server not found</AlertDescription>
        </Alert>
      </div>
    );
  }

  return (
    <div className="container mx-auto p-6 max-w-5xl">
      {/* Header */}
      <div className="flex items-center gap-4 mb-6">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => navigate('/sites')}
          className="gap-2"
        >
          <ChevronLeft className="h-4 w-4" />
          Back to Servers
        </Button>
      </div>

      <div className="mb-6">
        <h1 className="text-3xl font-bold">{server.display_name?.trim() || server.name}</h1>
        <p className="text-muted-foreground mt-1">
          Configure server settings, library locations, and storage paths
        </p>
      </div>

      <div className="space-y-6">
        {/* Download and Watch Folder Settings */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Download className="h-5 w-5" />
              Torrent Download Location
            </CardTitle>
            <CardDescription>
              Directory where downloaded torrent files will be temporarily stored
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div>
              <Label htmlFor="download-location">Download Path</Label>
              <div className="flex gap-2 mt-1.5">
                <Input
                  id="download-location"
                  value={downloadLocation}
                  onChange={(e) => setDownloadLocation(e.target.value)}
                  placeholder="/var/omnicloud/downloads"
                  className="font-mono text-sm"
                />
              </div>
              <p className="text-xs text-muted-foreground mt-1.5">
                This is where torrent data is downloaded before being moved to the library
              </p>
            </div>

            <div>
              <Label htmlFor="watch-folder">Watch Folder</Label>
              <div className="flex gap-2 mt-1.5">
                <Input
                  id="watch-folder"
                  value={watchFolder}
                  onChange={(e) => setWatchFolder(e.target.value)}
                  placeholder="/var/omnicloud/watch"
                  className="font-mono text-sm"
                />
              </div>
              <p className="text-xs text-muted-foreground mt-1.5">
                Monitor this folder for new DCPs to automatically ingest
              </p>
            </div>

            <div className="flex justify-end pt-2">
              <Button
                onClick={handleSaveBasicSettings}
                disabled={saving}
                className="gap-2"
              >
                <Save className="h-4 w-4" />
                {saving ? 'Saving...' : 'Save Settings'}
              </Button>
            </div>
          </CardContent>
        </Card>

        {/* Library Locations */}
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <FolderOpen className="h-5 w-5" />
              Library Locations
            </CardTitle>
            <CardDescription>
              Define multiple library paths where DCPs are stored and scanned
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* Existing Library Locations */}
            {libraryLocations.length > 0 ? (
              <div className="space-y-2">
                {libraryLocations.map((location) => (
                  <div
                    key={location.id}
                    className={cn(
                      "flex items-center gap-3 p-3 rounded-lg border",
                      location.is_active
                        ? "bg-card border-border"
                        : "bg-muted/50 border-muted"
                    )}
                  >
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2">
                        <h4 className="font-medium text-sm">{location.name}</h4>
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
                      </div>
                      <p className="text-xs text-muted-foreground font-mono mt-1 truncate">
                        {location.path}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleToggleLibraryLocation(location)}
                        className="h-8 px-2"
                      >
                        <Eye className="h-4 w-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleDeleteLibraryLocation(location.id, location.name)}
                        className="h-8 px-2 text-destructive hover:text-destructive"
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

            {/* Add New Library Location */}
            <div className="border-t pt-4 mt-4">
              <h4 className="font-medium text-sm mb-3">Add New Library Location</h4>
              <div className="space-y-3">
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
                    className="font-mono text-sm mt-1.5"
                  />
                </div>
                <div className="flex justify-end">
                  <Button
                    onClick={handleAddLibraryLocation}
                    disabled={!newLibraryName || !newLibraryPath}
                    className="gap-2"
                  >
                    <Plus className="h-4 w-4" />
                    Add Library Location
                  </Button>
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
