import { useState } from 'react';
import { Shield, Database, Users, ShieldCheck, Loader2, ScrollText } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog';
import { resetDb } from '@/api/admin';
import { toast } from 'sonner';
import { cn } from '@/lib/utils';
import UserManagement from '@/components/settings/UserManagement';
import RolePermissions from '@/components/settings/RolePermissions';
import ActivityLogView from '@/components/settings/ActivityLog';

type Section = 'users' | 'roles' | 'activity' | 'database';

const sections = [
  { id: 'users' as const, label: 'Users', icon: Users },
  { id: 'roles' as const, label: 'Role Permissions', icon: ShieldCheck },
  { id: 'activity' as const, label: 'Activity Log', icon: ScrollText },
  { id: 'database' as const, label: 'Database', icon: Database },
];

export default function Settings() {
  const [active, setActive] = useState<Section>('users');
  const [dbResetOpen, setDbResetOpen] = useState(false);
  const [resetting, setResetting] = useState(false);

  const handleDbReset = async () => {
    setResetting(true);
    try {
      await resetDb();
      toast.success('Database reset complete. Content, hashing, and torrents cleared.');
      setDbResetOpen(false);
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'response' in e
        ? (e as { response?: { data?: { error?: string } } }).response?.data?.error
        : (e as Error)?.message;
      toast.error(msg ?? 'Database reset failed');
    } finally {
      setResetting(false);
    }
  };

  return (
    <div className="flex gap-8">
      {/* Side menu */}
      <nav className="w-56 shrink-0 border-r border-gray-200 pr-6">
        <div className="flex items-center gap-2 mb-6">
          <Shield className="h-5 w-5 text-gray-600" />
          <h2 className="text-lg font-semibold text-gray-900">Settings</h2>
        </div>
        <ul className="space-y-0.5">
          {sections.map(sec => (
            <li key={sec.id}>
              <button
                onClick={() => setActive(sec.id)}
                className={cn(
                  'flex items-center gap-2 w-full px-3 py-2 text-sm text-left rounded-md transition-colors',
                  active === sec.id
                    ? 'bg-blue-50 text-blue-700 font-medium'
                    : 'text-gray-700 hover:bg-gray-100'
                )}
              >
                <sec.icon className="h-4 w-4" />
                {sec.label}
              </button>
            </li>
          ))}
        </ul>
      </nav>

      {/* Content area */}
      <div className="flex-1 min-w-0">
        {active === 'users' && <UserManagement />}
        {active === 'roles' && <RolePermissions />}
        {active === 'activity' && <ActivityLogView />}
        {active === 'database' && (
          <div>
            <h2 className="text-lg font-semibold text-gray-900 mb-1">Database</h2>
            <p className="text-sm text-gray-500 mb-6">
              Administrative database operations. Use with caution.
            </p>
            <div className="border rounded-lg p-4">
              <h3 className="font-medium text-gray-900 mb-1">Reset Database</h3>
              <p className="text-sm text-gray-500 mb-3">
                Clears all DCP content, hashing progress, and torrents. Registered servers (sites) and user accounts will be kept.
              </p>
              <Button variant="destructive" size="sm" onClick={() => setDbResetOpen(true)}>
                <Database className="h-4 w-4 mr-1" /> Reset Database
              </Button>
            </div>
          </div>
        )}
      </div>

      <AlertDialog open={dbResetOpen} onOpenChange={setDbResetOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Reset database?</AlertDialogTitle>
            <AlertDialogDescription>
              This will clear all DCP content, hashing progress, and torrents. Registered servers (sites) will be kept. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={resetting}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => { e.preventDefault(); handleDbReset(); }}
              disabled={resetting}
              className="bg-red-600 hover:bg-red-700"
            >
              {resetting ? <><Loader2 className="h-4 w-4 animate-spin" /> Resetting…</> : 'Reset database'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
