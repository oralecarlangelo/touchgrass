import type { Migration, MigrationRun, MigrationState, MigrationStatus, MigrationVerdict } from './api.ts';

// Wording for the Migrations tab. Kept apart from the components so
// the rules ("what does drift mean here") are tested without a DOM.

export type Tone = 'good' | 'warn' | 'bad' | 'neutral';

export const stateLabels: Record<MigrationState, string> = {
  applied: 'Applied',
  pending: 'Pending',
  failed: 'Failed',
  modified: 'Edited after apply',
  missing: 'Not in this build',
  rolled_back: 'Rolled back',
};

export const stateTones: Record<MigrationState, Tone> = {
  applied: 'good',
  pending: 'warn',
  failed: 'bad',
  modified: 'bad',
  missing: 'warn',
  rolled_back: 'neutral',
};

export const verdictLabels: Record<MigrationVerdict, string> = {
  in_sync: 'In sync',
  pending: 'Migrations pending',
  drift: 'Drift',
  failed: 'Failed migration',
  unknown: 'Unknown',
};

export const verdictTones: Record<MigrationVerdict, Tone> = {
  in_sync: 'good',
  pending: 'warn',
  drift: 'bad',
  failed: 'bad',
  unknown: 'neutral',
};

function plural(count: number, one: string, many: string): string {
  return `${count} ${count === 1 ? one : many}`;
}

// verdictSummary says in one sentence what the verdict means for this
// service and what to do about it.
export function verdictSummary(status: MigrationStatus): string {
  const { counts } = status;

  switch (status.verdict) {
    case 'failed':
      return `${plural(counts.failed, 'migration', 'migrations')} started and did not finish. Nothing further applies until it is fixed and marked applied or rolled back.`;
    case 'drift': {
      const parts: string[] = [];

      if (counts.modified > 0) {
        parts.push(`${plural(counts.modified, 'applied migration was', 'applied migrations were')} edited afterwards`);
      }

      if (counts.missing > 0) {
        parts.push(`${plural(counts.missing, 'applied migration is', 'applied migrations are')} not in the running build`);
      }

      return `${parts.join(' and ')}. The database and this build disagree about the schema's history.`;
    }
    case 'pending':
      return `The running build ships ${plural(counts.pending, 'migration', 'migrations')} the database has not applied.`;
    case 'in_sync':
      return status.shipped.ok
        ? `All ${plural(counts.applied, 'migration', 'migrations')} in the running build are applied.`
        : `${plural(counts.applied, 'migration is', 'migrations are')} applied. The running build could not be read, so pending and edited migrations cannot be told.`;
    default:
      return 'The migration history could not be read from the database.';
  }
}

// canResolve: only a migration recorded as failed may be marked
// applied or rolled back. The server enforces the same rule.
export function canResolve(migration: Migration): boolean {
  return migration.state === 'failed';
}

export function formatDuration(ms: number | undefined): string {
  if (ms === undefined) {
    return '';
  }

  if (ms < 1000) {
    return `${ms} ms`;
  }

  if (ms < 60_000) {
    return `${(ms / 1000).toFixed(1)} s`;
  }

  return `${Math.floor(ms / 60_000)} min ${Math.round((ms % 60_000) / 1000)} s`;
}

export function shortChecksum(checksum: string | undefined): string {
  return checksum === undefined || checksum === '' ? '' : checksum.slice(0, 12);
}

export const runKindLabels: Record<MigrationRun['kind'], string> = {
  deploy: 'Migrate deploy',
  resolve: 'Resolve',
  seed: 'Seed',
};

// lastSeed is the newest seed run, which is all the seed history there
// is: a seed leaves no record in the application's database.
export function lastSeed(runs: MigrationRun[]): MigrationRun | null {
  return runs.find((run) => run.kind === 'seed') ?? null;
}

export function hasRunningRun(runs: MigrationRun[]): boolean {
  return runs.some((run) => run.state === 'running');
}

// parseCommand splits the setup form's command line on whitespace. The
// server stores an argument vector and uses no shell, so quoting is
// not supported: an argument cannot contain a space.
export function parseCommand(line: string): string[] {
  return line.split(/\s+/).filter((part) => part !== '');
}
