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
import AccessFields, {
  accessProblem,
  defaultAccess,
  normalizeAccess,
} from './users/AccessFields.tsx';
import { describeScope, scopeLabel } from '@/lib/access.ts';
import {
  createUser,
  deleteUser,
  disableUser,
  enableUser,
  fetchUsers,
  isUnauthorized,
  resetUserPassword,
  setUserAccess,
  type ManagedUser,
  type ServiceView,
  type UserAccess,
} from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';

export default function UsersScreen({
  services,
  currentUsername,
  onUnauthorized,
}: {
  // Every managed service: the choices for a user's scope.
  services: ServiceView[];
  currentUsername: string;
  onUnauthorized: () => void;
}) {
  const [users, setUsers] = useState<ManagedUser[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [newAccess, setNewAccess] = useState<UserAccess>(defaultAccess);
  const [formError, setFormError] = useState<string | null>(null);
  const [working, setWorking] = useState(false);
  // The user whose access is being edited. It stays set while the
  // dialog closes, so the content does not change under the fade-out.
  const [editing, setEditing] = useState<ManagedUser | null>(null);
  const [accessOpen, setAccessOpen] = useState(false);
  const [editAccess, setEditAccess] = useState<UserAccess>(defaultAccess);
  const [editError, setEditError] = useState<string | null>(null);
  // Narrowing an all-services user is confirmed before it is saved.
  const [confirmingNarrow, setConfirmingNarrow] = useState(false);
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

    const problem = accessProblem(newAccess);

    if (problem !== null) {
      setFormError(problem);
      return;
    }

    setFormError(null);
    setWorking(true);

    try {
      const created = await createUser({
        username: username.trim(),
        password,
        ...normalizeAccess(newAccess),
      });
      toast.success(`User ${created.username} created. They must change the password at first login.`);
      setUsername('');
      setPassword('');
      setNewAccess(defaultAccess());
      setCreating(false);
      await load();
    } catch (err) {
      failed(err);
    } finally {
      setWorking(false);
    }
  }

  function openAccess(user: ManagedUser): void {
    setEditing(user);
    setEditAccess({ role: user.role, all_services: user.all_services, services: user.services });
    setEditError(null);
    setConfirmingNarrow(false);
    setAccessOpen(true);
  }

  async function handleSaveAccess(): Promise<void> {
    if (editing === null) {
      return;
    }

    const problem = accessProblem(editAccess);

    if (problem !== null) {
      setEditError(problem);
      return;
    }

    const next = normalizeAccess(editAccess);

    // Going from every service to a list takes the host screens and
    // every other service away at once, so it is confirmed first.
    if (editing.all_services && !next.all_services && !confirmingNarrow) {
      setEditError(null);
      setConfirmingNarrow(true);
      return;
    }

    setEditError(null);
    setWorking(true);

    try {
      const updated = await setUserAccess(editing.id, next);
      toast.success(`${updated.username} is now ${updated.role} with ${scopeLabel(updated)}.`);
      setAccessOpen(false);
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
      toast.success(
        `Temporary password set for ${pendingPassword.username}. Their sessions were ended.`,
      );
      setPendingPassword(null);
      setNewPassword('');
      await load();
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
            A role decides what a user may do; the services decide where. Admins run everything,
            everywhere. Viewers read, and developers also deploy, on every service or only the ones
            you select.
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
                  <TableHead>Access</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {users.map((user) => (
                  <TableRow key={user.id}>
                    <TableCell className="font-medium">
                      {user.username}
                      {user.username === currentUsername && (
                        <span className="text-muted-foreground ml-1 text-xs font-normal">(you)</span>
                      )}
                    </TableCell>
                    <TableCell>
                      <Badge variant={user.role === 'admin' ? 'default' : 'secondary'}>
                        {user.role}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-sm" title={user.services.join(', ')}>
                      {describeScope(user)}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {user.disabled ? (
                          <Badge variant="destructive">disabled</Badge>
                        ) : (
                          <Badge variant="outline">active</Badge>
                        )}
                        {user.must_change_password && (
                          <Badge
                            variant="secondary"
                            title="On a temporary password: they must replace it at their next login"
                          >
                            temporary password
                          </Badge>
                        )}
                      </div>
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
                          onClick={() => openAccess(user)}
                          aria-label={`Change access of ${user.username}`}
                        >
                          Access
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
              Viewers are read-only: good for people who need the logs. Developers can also deploy
              the services you give them. Admins can do everything, including manage users. The
              password you set is temporary: the user must replace it at their first login, so pass
              it to them directly.
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
              <Label htmlFor="new-password">Temporary password</Label>
              <Input
                id="new-password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="new-password"
              />
            </div>
            <AccessFields
              idPrefix="new"
              access={newAccess}
              onChange={setNewAccess}
              services={services}
            />
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

      <Dialog open={accessOpen} onOpenChange={(open) => !open && setAccessOpen(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Access for {editing?.username}</DialogTitle>
            <DialogDescription>
              The change applies to their next request. Their open pages follow within seconds.
            </DialogDescription>
          </DialogHeader>
          {confirmingNarrow && editing !== null ? (
            <div role="alert" className="border-border space-y-2 rounded-md border p-3 text-sm">
              <p className="font-medium">
                Limit {editing.username} to {scopeLabel(normalizeAccess(editAccess))}?
              </p>
              <p className="text-muted-foreground">
                They have every service today. Saving takes away every other service and the host
                screens (Fleet, Security, Databases, Images, API keys, the full audit trail) at
                once. Services added later will not be visible to them until you grant them.
              </p>
            </div>
          ) : (
            <AccessFields
              idPrefix="edit"
              access={editAccess}
              onChange={setEditAccess}
              services={services}
            />
          )}
          {editError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {editError}
            </p>
          )}
          <DialogFooter>
            {confirmingNarrow ? (
              <Button variant="outline" onClick={() => setConfirmingNarrow(false)}>
                Back
              </Button>
            ) : (
              <Button variant="outline" onClick={() => setAccessOpen(false)}>
                Cancel
              </Button>
            )}
            <Button onClick={() => void handleSaveAccess()} disabled={working}>
              {working ? 'Saving…' : confirmingNarrow ? 'Limit access' : 'Save access'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={pendingPassword !== null} onOpenChange={(open) => !open && setPendingPassword(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Reset password</DialogTitle>
            <DialogDescription>
              Set a temporary password for {pendingPassword?.username}. Their sessions end now, and
              they must replace it at their next login.
              {pendingPassword?.username === currentUsername &&
                ' This is your own account: you will be logged out. To keep your session, use Change password in the account menu instead.'}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-1">
            <Label htmlFor="reset-password">Temporary password</Label>
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
