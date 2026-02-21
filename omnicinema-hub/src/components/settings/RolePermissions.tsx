import { useState, useEffect } from 'react';
import { usersApi, RolePermission } from '@/api/users';
import { ALL_PAGES, ROLE_LABELS } from '@/lib/permissions';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { toast } from 'sonner';
import { Loader2, Save, ShieldCheck } from 'lucide-react';

export default function RolePermissions() {
  const [roles, setRoles] = useState<RolePermission[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<string | null>(null);
  const [dirty, setDirty] = useState<Record<string, string[]>>({});

  const fetchRoles = async () => {
    try {
      const data = await usersApi.listRoles();
      setRoles(Array.isArray(data) ? data : []);
      setDirty({});
    } catch {
      toast.error('Failed to load roles');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchRoles(); }, []);

  const getPages = (role: RolePermission): string[] => {
    if (role.role === 'admin') return ALL_PAGES.map(p => p.id);
    return dirty[role.role] ?? role.allowed_pages;
  };

  const togglePage = (role: RolePermission, pageId: string) => {
    if (role.role === 'admin') return;
    const current = getPages(role);
    const next = current.includes(pageId)
      ? current.filter(p => p !== pageId)
      : [...current, pageId];
    setDirty(d => ({ ...d, [role.role]: next }));
  };

  const handleSave = async (role: RolePermission) => {
    const pages = dirty[role.role];
    if (!pages) return;
    setSaving(role.role);
    try {
      await usersApi.updateRolePermissions(role.role, {
        allowed_pages: pages,
        description: role.description,
      });
      toast.success(`Permissions updated for "${ROLE_LABELS[role.role] ?? role.role}"`);
      setDirty(d => {
        const next = { ...d };
        delete next[role.role];
        return next;
      });
      fetchRoles();
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'response' in e
        ? (e as { response?: { data?: { error?: string } } }).response?.data?.error
        : (e as Error)?.message;
      toast.error(msg ?? 'Failed to update permissions');
    } finally {
      setSaving(null);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-6 w-6 animate-spin text-gray-400" />
      </div>
    );
  }

  return (
    <div>
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-gray-900">Role Permissions</h2>
        <p className="text-sm text-gray-500">Configure which pages each role can access</p>
      </div>

      <div className="space-y-6">
        {roles.map(role => {
          const isAdmin = role.role === 'admin';
          const pages = getPages(role);
          const isDirty = !!dirty[role.role];
          const isSaving = saving === role.role;

          return (
            <div key={role.role} className="border rounded-lg p-4">
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-2">
                  <ShieldCheck className={`h-5 w-5 ${isAdmin ? 'text-blue-600' : 'text-gray-500'}`} />
                  <h3 className="font-semibold text-gray-900">{ROLE_LABELS[role.role] ?? role.role}</h3>
                  {isAdmin && <span className="text-xs text-blue-600 bg-blue-50 px-2 py-0.5 rounded-full">Full Access</span>}
                </div>
                {!isAdmin && (
                  <Button
                    size="sm"
                    onClick={() => handleSave(role)}
                    disabled={!isDirty || isSaving}
                  >
                    {isSaving ? <Loader2 className="h-4 w-4 animate-spin mr-1" /> : <Save className="h-4 w-4 mr-1" />}
                    {isSaving ? 'Saving...' : 'Save'}
                  </Button>
                )}
              </div>
              {role.description && (
                <p className="text-sm text-gray-500 mb-3">{role.description}</p>
              )}
              <div className="grid grid-cols-3 gap-3">
                {ALL_PAGES.map(page => {
                  const checked = pages.includes(page.id);
                  const disabled = isAdmin || page.id === 'dashboard';
                  return (
                    <div key={page.id} className="flex items-center gap-2">
                      <Checkbox
                        id={`${role.role}-${page.id}`}
                        checked={checked}
                        disabled={disabled}
                        onCheckedChange={() => togglePage(role, page.id)}
                      />
                      <Label
                        htmlFor={`${role.role}-${page.id}`}
                        className={`text-sm ${disabled ? 'text-gray-400' : 'text-gray-700'} cursor-pointer`}
                      >
                        {page.label}
                        {page.id === 'dashboard' && !isAdmin && (
                          <span className="text-xs text-gray-400 ml-1">(always on)</span>
                        )}
                      </Label>
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}
