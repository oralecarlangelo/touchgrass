import { useState, type FormEvent, type ReactNode } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { ApiError, changeOwnPassword } from '@/lib/api';
import { canSubmitPassword, minPasswordLength, newPasswordProblem } from '@/lib/password';

// Turns a refused change into a sentence about the field at fault.
export function passwordChangeError(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'invalid_current_password') {
      return 'The current password is not right.';
    }

    if (err.status === 429) {
      return 'Too many attempts. Wait a minute, then try again.';
    }
  }

  return err instanceof Error ? err.message : String(err);
}

// The shared change-password form. `knownCurrent` is the password the
// user typed at login moments ago (the forced first-login change); when
// set, the current-password field is not shown again.
export default function PasswordFields({
  idPrefix,
  username,
  knownCurrent = '',
  submitLabel,
  onChanged,
  secondaryAction,
}: {
  idPrefix: string;
  username: string;
  knownCurrent?: string;
  submitLabel: string;
  onChanged: () => void;
  secondaryAction?: ReactNode;
}) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const askCurrent = knownCurrent === '';
  const effectiveCurrent = askCurrent ? current : knownCurrent;
  const problem = newPasswordProblem(next, confirm, effectiveCurrent);
  const ready = canSubmitPassword(next, confirm, effectiveCurrent);

  async function handleSubmit(event: FormEvent<HTMLFormElement>): Promise<void> {
    event.preventDefault();

    if (!ready || submitting) {
      return;
    }

    setSubmitting(true);
    setError(null);

    try {
      await changeOwnPassword(effectiveCurrent, next);
      onChanged();
    } catch (err) {
      setError(passwordChangeError(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={(event) => void handleSubmit(event)}
      aria-label="Change password"
      className="space-y-4"
    >
      {/* Lets a password manager file the new password under the right account. */}
      <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />

      {askCurrent && (
        <div className="grid gap-2">
          <Label htmlFor={`${idPrefix}-current`}>Current password</Label>
          <Input
            id={`${idPrefix}-current`}
            type="password"
            value={current}
            onChange={(event) => setCurrent(event.target.value)}
            autoComplete="current-password"
          />
        </div>
      )}

      <div className="grid gap-2">
        <Label htmlFor={`${idPrefix}-new`}>New password</Label>
        <Input
          id={`${idPrefix}-new`}
          type="password"
          value={next}
          onChange={(event) => setNext(event.target.value)}
          autoComplete="new-password"
          aria-describedby={`${idPrefix}-hint`}
          aria-invalid={problem !== null}
        />
      </div>

      <div className="grid gap-2">
        <Label htmlFor={`${idPrefix}-confirm`}>Repeat the new password</Label>
        <Input
          id={`${idPrefix}-confirm`}
          type="password"
          value={confirm}
          onChange={(event) => setConfirm(event.target.value)}
          autoComplete="new-password"
          aria-describedby={`${idPrefix}-hint`}
        />
      </div>

      <p
        id={`${idPrefix}-hint`}
        className={problem === null ? 'text-muted-foreground text-sm' : 'text-destructive text-sm'}
      >
        {problem ??
          `At least ${minPasswordLength} characters. A password manager's generated one is ideal.`}
      </p>

      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}

      <div className="flex items-center justify-between gap-2">
        {secondaryAction ?? <span />}
        <Button type="submit" disabled={!ready || submitting}>
          {submitting ? 'Saving…' : submitLabel}
        </Button>
      </div>
    </form>
  );
}
