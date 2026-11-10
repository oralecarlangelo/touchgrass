import type { FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

export default function LoginForm({
  username,
  onUsernameChange,
  password,
  onPasswordChange,
  onSubmit,
  submitting,
  error,
}: {
  username: string;
  onUsernameChange: (value: string) => void;
  password: string;
  onPasswordChange: (value: string) => void;
  onSubmit: () => void;
  submitting: boolean;
  error: string | null;
}) {
  function handleSubmit(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    onSubmit();
  }

  return (
    <div className="bg-background flex min-h-screen items-center justify-center px-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>touchgrass</CardTitle>
          <CardDescription>Log in. No vibe check without it.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} aria-label="Login" className="space-y-4">
            <div className="grid gap-2">
              <Label htmlFor="login-username">Username</Label>
              <Input
                id="login-username"
                type="text"
                value={username}
                onChange={(event) => onUsernameChange(event.target.value)}
                autoComplete="username"
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="login-password">Password</Label>
              <Input
                id="login-password"
                type="password"
                value={password}
                onChange={(event) => onPasswordChange(event.target.value)}
                autoComplete="current-password"
              />
            </div>

            {error !== null && (
              <p role="alert" className="text-destructive text-sm">
                {error}
              </p>
            )}

            <Button type="submit" disabled={submitting || password === ''} className="w-full">
              {submitting ? 'Checking…' : 'Log in'}
            </Button>

            <details className="text-muted-foreground text-sm">
              <summary className="hover:text-foreground cursor-pointer select-none">
                First time on this install?
              </summary>
              <p className="mt-2">
                If no admin password was configured, touchgrass generated a one-time password on its
                first start. Sign in as <code className="text-foreground">admin</code> with the
                contents of the <code className="text-foreground">initial-admin-password</code> file
                beside the database; you choose your own password right after.
              </p>
              <p className="mt-2">
                Lost it? On the host, run{' '}
                <code className="text-foreground">touchgrass users reset-password</code>.
              </p>
            </details>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
