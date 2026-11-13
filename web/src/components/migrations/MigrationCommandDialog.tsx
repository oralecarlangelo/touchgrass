import { useEffect, useState } from 'react';

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
import { confirmationMatches } from '../env/EnvReviewDialog.tsx';

// PendingCommand is a migrate or seed command waiting for the admin to
// confirm it.
export type PendingCommand =
  | { kind: 'deploy'; pending: number }
  | { kind: 'seed'; command: string }
  | { kind: 'resolve'; migration: string; resolution: 'applied' | 'rolled_back' };

export function commandTitle(command: PendingCommand): string {
  switch (command.kind) {
    case 'deploy':
      return 'Apply pending migrations';
    case 'seed':
      return 'Run the seed';
    default:
      return command.resolution === 'applied' ? 'Mark migration as applied' : 'Mark migration as rolled back';
  }
}

// commandEffect says what the command changes, in terms of the
// application's database rather than the tool's flags.
export function commandEffect(command: PendingCommand, database: string): string {
  switch (command.kind) {
    case 'deploy':
      return `Applies ${command.pending} pending ${command.pending === 1 ? 'migration' : 'migrations'} to ${database}, in order. A migration that fails stops the run and leaves the schema partly changed.`;
    case 'seed':
      return `Runs "${command.command}" against ${database}. What it writes depends on the seed script: a seed that is not safe to repeat will duplicate or overwrite data.`;
    default:
      return command.resolution === 'applied'
        ? `Records ${command.migration} as applied without running it. Do this only after its SQL has been completed by hand.`
        : `Records ${command.migration} as rolled back so it can be applied again. Do this only after its partial changes have been undone by hand.`;
  }
}

export default function MigrationCommandDialog({
  command,
  serviceName,
  database,
  container,
  busy,
  error,
  onClose,
  onConfirm,
}: {
  command: PendingCommand | null;
  serviceName: string;
  database: string;
  container: string;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onConfirm: (confirm: string) => void;
}) {
  const [confirm, setConfirm] = useState('');

  useEffect(() => {
    setConfirm('');
  }, [command]);

  if (command === null) {
    return null;
  }

  const ready = confirmationMatches(confirm, serviceName) && !busy;

  return (
    <Dialog open onOpenChange={(next) => { if (!next && !busy) onClose(); }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{commandTitle(command)}</DialogTitle>
          <DialogDescription>{commandEffect(command, database)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-muted-foreground text-sm">
            It runs inside{' '}
            {container === '' ? "the service's container" : <span className="font-mono text-xs">{container}</span>} with the application's own
            environment, and is recorded in the audit log under your name.
          </p>
          <div className="space-y-1.5">
            <Label htmlFor="migration-confirm">
              Type <span className="font-mono">{serviceName}</span> to confirm
            </Label>
            <Input
              id="migration-confirm"
              value={confirm}
              disabled={busy}
              autoComplete="off"
              onChange={(event) => setConfirm(event.target.value)}
            />
          </div>
          {error !== null && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button type="button" disabled={!ready} onClick={() => onConfirm(confirm.trim())}>
            {busy ? 'Starting…' : commandTitle(command)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
