import { useState } from 'react';
import { Package, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

interface CreateBuildDialogProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: () => void;
  createBuildMutation: { mutateAsync: (v: string) => Promise<unknown>; isPending: boolean };
}

const defaultVersion = () => {
  const now = new Date();
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, '0');
  const d = String(now.getDate()).padStart(2, '0');
  const h = String(now.getHours()).padStart(2, '0');
  const min = String(now.getMinutes()).padStart(2, '0');
  const s = String(now.getSeconds()).padStart(2, '0');
  return `${y}${m}${d}-${h}${min}${s}`;
};

export default function CreateBuildDialog({
  isOpen,
  onClose,
  onSuccess,
  createBuildMutation,
}: CreateBuildDialogProps) {
  const [versionName, setVersionName] = useState(defaultVersion());

  const handleOpenChange = (open: boolean) => {
    if (!open) onClose();
    if (open) setVersionName(defaultVersion());
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const name = versionName.trim() || defaultVersion();
    try {
      await createBuildMutation.mutateAsync(name);
      onSuccess();
      onClose();
    } catch {
      // Error toast is handled by the caller
    }
  };

  return (
    <Dialog open={isOpen} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Package className="h-5 w-5" />
            Create new build
          </DialogTitle>
          <DialogDescription>
            Build the backend from source and register it as an available upgrade for clients.
            Enter a version name (e.g. 20260214-235000) or leave blank for current time.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit}>
          <div className="grid gap-4 py-4">
            <div className="grid gap-2">
              <Label htmlFor="version_name">Version / build name</Label>
              <Input
                id="version_name"
                value={versionName}
                onChange={(e) => setVersionName(e.target.value)}
                placeholder={defaultVersion()}
                className="font-mono"
                disabled={createBuildMutation.isPending}
              />
              <p className="text-xs text-muted-foreground">
                Letters, numbers, dots, hyphens, underscores only. Clients will see this as the upgrade version.
              </p>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose} disabled={createBuildMutation.isPending}>
              Cancel
            </Button>
            <Button type="submit" disabled={createBuildMutation.isPending}>
              {createBuildMutation.isPending ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Building…
                </>
              ) : (
                'Build & publish'
              )}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
