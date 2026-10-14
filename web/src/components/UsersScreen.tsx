import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from './ErrorState.tsx';
import {
  createUser,
  deleteUser,
  disableUser,
  enableUser,
  fetchUsers,
  isUnauthorized,
  resetUserPassword,
  setUserRole,
  type ManagedUser,
} from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';

export default function UsersScreen({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [users, setUsers] = useState<ManagedUser[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState('viewer');
  const [formError, setFormError] = useState<string | null>(null);
  const [working, setWorking] = useState(false);
  const [pendingPassword, setPendingPassword] = useState<ManagedUser | null>(null);
  const [newPassword, setNewPassword] = useState('');
  const [pendingDelete, setPendingDelete] = useState<ManagedUser | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      setUsers(await fetchUsers());
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  function failed(err: unknown): void {
    if (isUnauthorized(err)) {
      onUnauthorized();
      return;
    }

    toast.error(err instanceof Error ? err.message : String(err));
  }

  async function handleCreate(): Promise<void> {
    if (username.trim() === '' || password === '') {
      setFormError('Username and password are required.');
      return;
    }

    setFormError(null);
    setWorking(true);

    try {
      const created = await createUser({ username: username.trim(), password, role });
      toast.success(`User ${created.username} created.`);
      setUsername('');
      setPassword('');
      setRole('viewer');
      setCreating(false);
      await load();
    } catch (err) {
      failed(err);
    } finally {
      setWorking(false);
    }
  }

  async function handleRole(user: ManagedUser): Promise<void> {
    const next = user.role === 'admin' ? 'viewer' : 'admin';
    setWorking(true);

    try {
      await setUserRole(user.id, next);
      toast.success(`${user.username} is now ${next}.`);
      await load();
    } catch (err) {
      failed(err);
    } finally {
      setWorking(false);
    }
  }

  async function handleResetPassword(): Promise<void> {
    if (pendingPassword === null || newPassword === '') {
      return;
    }

    setWorking(true);

    try {
      await resetUserPassword(pendingPassword.id, newPassword);
      toast.success(`Password reset for ${pendingPassword.username}.`);
      setPendingPassword(null);
      setNewPassword('');
    } catch (err) {
      failed(err);
    } finally {
      setWorking(false);
    }
  }

  async function handleDisabled(user: ManagedUser): Promise<void> {
    setWorking(true);

    try {
      if (user.disabled) {
        await enableUser(user.id);
        toast.success(`${user.username} enabled.`);
      } else {
        await disableUser(user.id);
        toast.success(`${user.username} disabled.`);
      }

      await load();
    } catch (err) {
      failed(err);
    } finally {
      setWorking(false);
    }
  }

  async function handleDelete(): Promise<void> {
    if (pendingDelete === null) {
      return;
    }

    setWorking(true);

    try {
      await deleteUser(pendingDelete.id);
      toast.success(`User ${pendingDelete.username} deleted.`);
      setPendingDelete(null);
      await load();
    } catch (err) {
      failed(err);
    } finally {
      setWorking(false);
    }
  }

  if (error !== null) {
    return <ErrorState title="Couldn't load users" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Users</h1>
        <Button size="sm" className="ml-auto" onClick={() => setCreating(true)}>
          New user
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">
            Admins run everything; viewers read logs, metrics, and history
          </CardTitle>
        </CardHeader>
        <CardContent>
          {users === null ? (
            <div className="space-y-2" aria-label="Loading users">
              <Skeleton className="h-10 w-full" />
            </div>
          ) : users.length === 0 ? (
            <p className="text-muted-foreground text-sm">No users — this shouldn&apos;t happen.</p>
          ) : (
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
                {users.map((user) => (
                  <TableRow key={user.id}>
                    <TableCell className="font-medium">{user.username}</TableCell>
                    <TableCell>
                      <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                        {user.role}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      {user.disabled ? (
                        <Badge variant="destructive">disabled</Badge>
                      ) : (
                        <Badge variant="outline">active</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {formatTime(user.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={working}
                          onClick={() => void handleRole(user)}
                          aria-label={`Change role of ${user.username}`}
                        >
                          {user.role === 'admin' ? 'Demote' : 'Promote'}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => {
                            setPendingPassword(user);
                            setNewPassword('');
                          }}
                          aria-label={`Reset password of ${user.username}`}
                        >
                          Password
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={working}
                          onClick={() => void handleDisabled(user)}
                          aria-label={`${user.disabled ? 'Enable' : 'Disable'} ${user.username}`}
                        >
                          {user.disabled ? 'Enable' : 'Disable'}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:text-destructive"
                          onClick={() => setPendingDelete(user)}
                          aria-label={`Delete ${user.username}`}
                        >
                          Delete
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog open={creating} onOpenChange={(open) => !open && setCreating(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New user</DialogTitle>
            <DialogDescription>
              Viewers are read-only — good for devs who need the logs. Admins can deploy and
              manage users.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="new-username">Username</Label>
              <Input
                id="new-username"
                value={username}
                onChange={(event) => setUsername(event.target.value)}
                placeholder="bob"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="new-password">Password</Label>
              <Input
                id="new-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="new-password"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="new-role">Role</Label>
              <Select value={role} onValueChange={setRole}>
                <SelectTrigger id="new-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="viewer">viewer (read-only)</SelectItem>
                  <SelectItem value="admin">admin (full power)</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {formError !== null && (
              <p role="alert" className="text-destructive text-sm">
                {formError}
              </p>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCreating(false)}>
              Cancel
            </Button>
            <Button onClick={() => void handleCreate()} disabled={working}>
              {working ? 'Creating…' : 'Create user'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={pendingPassword !== null} onOpenChange={(open) => !open && setPendingPassword(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Reset password</DialogTitle>
            <DialogDescription>
              Set a new password for {pendingPassword?.username}. Their sessions keep working —
              disable the account first to lock them out.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-1">
            <Label htmlFor="reset-password">New password</Label>
            <Input
              id="reset-password"
              type="password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              autoComplete="new-password"
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingPassword(null)}>
              Cancel
            </Button>
            <Button onClick={() => void handleResetPassword()} disabled={working || newPassword === ''}>
              {working ? 'Resetting…' : 'Reset password'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete {pendingDelete?.username}?</DialogTitle>
            <DialogDescription>
              Their login stops working immediately. Audit history keeps their name — that part
              is forever.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingDelete(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void handleDelete()} disabled={working}>
              {working ? 'Deleting…' : 'Delete user'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
