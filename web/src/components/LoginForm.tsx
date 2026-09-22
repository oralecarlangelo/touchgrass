import type { FormEvent } from 'react';

import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';

export default function LoginForm({
  password,
  onPasswordChange,
  onSubmit,
  submitting,
  error,
}: {
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
          <CardDescription>Admin login. No vibe check without it.</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} aria-label="Admin login" className="space-y-4">
            <div className="grid gap-2">
              <Label htmlFor="admin-password">Admin password</Label>
              <Input
                id="admin-password"
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
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
