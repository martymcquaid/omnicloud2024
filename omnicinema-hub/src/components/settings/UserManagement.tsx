import { useState, useEffect } from 'react';
import { usersApi, UserRecord } from '@/api/users';
import { useAuth } from '@/contexts/AuthContext';
import { ROLE_LABELS } from '@/lib/permissions';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Badge } from '@/components/ui/badge';
import { Switch } from '@/components/ui/switch';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { toast } from 'sonner';
import { UserPlus, Pencil, Trash2, Key, Loader2 } from 'lucide-react';

const ROLES = Object.entries(ROLE_LABELS);

export default function UserManagement() {
  const { username: currentUser } = useAuth();
  const [users, setUsers] = useState<UserRecord[]>([]);
  const [loading, setLoading] = useState(true);

  // Add dialog
  const [addOpen, setAddOpen] = useState(false);
  const [addForm, setAddForm] = useState({ username: '', password: '', role: 'manager' });
  const [addBusy, setAddBusy] = useState(false);

  // Edit dialog
  const [editOpen, setEditOpen] = useState(false);
  const [editUser, setEditUser] = useState<UserRecord | null>(null);
  const [editForm, setEditForm] = useState({ username: '', role: '', is_active: true });
  const [editBusy, setEditBusy] = useState(false);

  // Change password dialog
  const [pwOpen, setPwOpen] = useState(false);
  const [pwUser, setPwUser] = useState<UserRecord | null>(null);
  const [newPassword, setNewPassword] = useState('');
  const [pwBusy, setPwBusy] = useState(false);

  // Delete dialog
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleteUser, setDeleteUser] = useState<UserRecord | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  const fetchUsers = async () => {
    try {
      const data = await usersApi.list();
      setUsers(Array.isArray(data) ? data : []);
    } catch {
      toast.error('Failed to load users');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchUsers(); }, []);

  const handleAdd = async () => {
    if (!addForm.username || !addForm.password) {
      toast.error('Username and password are required');
      return;
    }
    setAddBusy(true);
    try {
      await usersApi.create(addForm);
      toast.success(`User "${addForm.username}" created`);
      setAddOpen(false);
      setAddForm({ username: '', password: '', role: 'manager' });
      fetchUsers();
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'response' in e
        ? (e as { response?: { data?: { error?: string } } }).response?.data?.error
        : (e as Error)?.message;
      toast.error(msg ?? 'Failed to create user');
    } finally {
      setAddBusy(false);
    }
  };

  const openEdit = (user: UserRecord) => {
    setEditUser(user);
    setEditForm({ username: user.username, role: user.role, is_active: user.is_active });
    setEditOpen(true);
  };

  const handleEdit = async () => {
    if (!editUser) return;
    setEditBusy(true);
    try {
      await usersApi.update(editUser.id, editForm);
      toast.success(`User "${editForm.username}" updated`);
      setEditOpen(false);
      fetchUsers();
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'response' in e
        ? (e as { response?: { data?: { error?: string } } }).response?.data?.error
        : (e as Error)?.message;
      toast.error(msg ?? 'Failed to update user');
    } finally {
      setEditBusy(false);
    }
  };

  const openChangePassword = (user: UserRecord) => {
    setPwUser(user);
    setNewPassword('');
    setPwOpen(true);
  };

  const handleChangePassword = async () => {
    if (!pwUser || !newPassword) return;
    setPwBusy(true);
    try {
      await usersApi.changePassword(pwUser.id, newPassword);
      toast.success(`Password changed for "${pwUser.username}"`);
      setPwOpen(false);
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'response' in e
        ? (e as { response?: { data?: { error?: string } } }).response?.data?.error
        : (e as Error)?.message;
      toast.error(msg ?? 'Failed to change password');
    } finally {
      setPwBusy(false);
    }
  };

  const openDelete = (user: UserRecord) => {
    setDeleteUser(user);
    setDeleteOpen(true);
  };

  const handleDelete = async () => {
    if (!deleteUser) return;
    setDeleteBusy(true);
    try {
      await usersApi.delete(deleteUser.id);
      toast.success(`User "${deleteUser.username}" deleted`);
      setDeleteOpen(false);
      fetchUsers();
    } catch (e: unknown) {
      const msg = e && typeof e === 'object' && 'response' in e
        ? (e as { response?: { data?: { error?: string } } }).response?.data?.error
        : (e as Error)?.message;
      toast.error(msg ?? 'Failed to delete user');
    } finally {
      setDeleteBusy(false);
    }
  };

  const isSelf = (user: UserRecord) => user.username === currentUser;

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <Loader2 className="h-6 w-6 animate-spin text-gray-400" />
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-lg font-semibold text-gray-900">Users</h2>
          <p className="text-sm text-gray-500">Manage user accounts and assign roles</p>
        </div>
        <Button onClick={() => setAddOpen(true)} size="sm">
          <UserPlus className="h-4 w-4 mr-1" /> Add User
        </Button>
      </div>

      <div className="border rounded-lg">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Username</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Created</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.map(user => (
              <TableRow key={user.id}>
                <TableCell className="font-medium">
                  {user.username}
                  {isSelf(user) && <Badge variant="outline" className="ml-2 text-xs">You</Badge>}
                </TableCell>
                <TableCell>
                  <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                    {ROLE_LABELS[user.role] ?? user.role}
                  </Badge>
                </TableCell>
                <TableCell>
                  <Badge variant={user.is_active ? 'default' : 'destructive'} className={user.is_active ? 'bg-green-600' : ''}>
                    {user.is_active ? 'Active' : 'Inactive'}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm text-gray-500">
                  {new Date(user.created_at).toLocaleDateString('en-GB')}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    <Button variant="ghost" size="sm" onClick={() => openEdit(user)} title="Edit">
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button variant="ghost" size="sm" onClick={() => openChangePassword(user)} title="Change Password">
                      <Key className="h-4 w-4" />
                    </Button>
                    {!isSelf(user) && (
                      <Button variant="ghost" size="sm" onClick={() => openDelete(user)} title="Delete" className="text-red-600 hover:text-red-700">
                        <Trash2 className="h-4 w-4" />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Add User Dialog */}
      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add User</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="add-username">Username</Label>
              <Input id="add-username" value={addForm.username} onChange={e => setAddForm(f => ({ ...f, username: e.target.value }))} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="add-password">Password</Label>
              <Input id="add-password" type="password" value={addForm.password} onChange={e => setAddForm(f => ({ ...f, password: e.target.value }))} />
            </div>
            <div className="space-y-2">
              <Label>Role</Label>
              <Select value={addForm.role} onValueChange={v => setAddForm(f => ({ ...f, role: v }))}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {ROLES.map(([value, label]) => (
                    <SelectItem key={value} value={value}>{label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddOpen(false)} disabled={addBusy}>Cancel</Button>
            <Button onClick={handleAdd} disabled={addBusy}>
              {addBusy ? <><Loader2 className="h-4 w-4 animate-spin mr-1" /> Creating...</> : 'Create User'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit User Dialog */}
      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit User</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="edit-username">Username</Label>
              <Input id="edit-username" value={editForm.username} onChange={e => setEditForm(f => ({ ...f, username: e.target.value }))} />
            </div>
            <div className="space-y-2">
              <Label>Role</Label>
              <Select value={editForm.role} onValueChange={v => setEditForm(f => ({ ...f, role: v }))}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {ROLES.map(([value, label]) => (
                    <SelectItem key={value} value={value}>{label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            {editUser && !isSelf(editUser) && (
              <div className="flex items-center justify-between">
                <Label htmlFor="edit-active">Active</Label>
                <Switch id="edit-active" checked={editForm.is_active} onCheckedChange={v => setEditForm(f => ({ ...f, is_active: v }))} />
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditOpen(false)} disabled={editBusy}>Cancel</Button>
            <Button onClick={handleEdit} disabled={editBusy}>
              {editBusy ? <><Loader2 className="h-4 w-4 animate-spin mr-1" /> Saving...</> : 'Save Changes'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Change Password Dialog */}
      <Dialog open={pwOpen} onOpenChange={setPwOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Change Password for {pwUser?.username}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 py-2">
            <div className="space-y-2">
              <Label htmlFor="new-password">New Password</Label>
              <Input id="new-password" type="password" value={newPassword} onChange={e => setNewPassword(e.target.value)} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPwOpen(false)} disabled={pwBusy}>Cancel</Button>
            <Button onClick={handleChangePassword} disabled={pwBusy || !newPassword}>
              {pwBusy ? <><Loader2 className="h-4 w-4 animate-spin mr-1" /> Changing...</> : 'Change Password'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete User Dialog */}
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete user "{deleteUser?.username}"?</AlertDialogTitle>
            <AlertDialogDescription>
              This will permanently delete the user account and invalidate all their sessions. This action cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleteBusy}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={e => { e.preventDefault(); handleDelete(); }}
              disabled={deleteBusy}
              className="bg-red-600 hover:bg-red-700"
            >
              {deleteBusy ? <><Loader2 className="h-4 w-4 animate-spin" /> Deleting...</> : 'Delete User'}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
