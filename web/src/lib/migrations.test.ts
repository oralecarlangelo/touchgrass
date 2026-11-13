import { describe, expect, it } from 'vitest';

import type { MigrationRun, MigrationStatus } from './api.ts';
import {
  canResolve,
  formatDuration,
  hasRunningRun,
  lastSeed,
  parseCommand,
  shortChecksum,
  verdictSummary,
} from './migrations.ts';

function status(overrides: Partial<MigrationStatus>): MigrationStatus {
  return {
    configured: true,
    tool: 'prisma',
    verdict: 'in_sync',
    history: { ok: true, target: 'db:5432/shop (schema public)' },
    shipped: { ok: true, target: 'shop-api-1:/app/prisma/migrations' },
    counts: { applied: 0, pending: 0, failed: 0, modified: 0, missing: 0, rolled_back: 0 },
    migrations: [],
    seed: { configured: false },
    checked_at: '2026-11-13T09:00:00Z',
    ...overrides,
  };
}

function run(overrides: Partial<MigrationRun>): MigrationRun {
  return {
    id: 1,
    service_id: 'shop-api',
    kind: 'deploy',
    tool: 'prisma',
    detail: 'npx --no-install prisma migrate deploy',
    actor: 'owner',
    state: 'succeeded',
    container: 'shop-api-1',
    image: 'shop/api:1',
    started_at: '2026-11-13T09:00:00Z',
    ...overrides,
  };
}

describe('verdictSummary', () => {
  it('counts what is pending', () => {
    const text = verdictSummary(
      status({ verdict: 'pending', counts: { applied: 4, pending: 2, failed: 0, modified: 0, missing: 0, rolled_back: 0 } }),
    );

    expect(text).toBe('The running build ships 2 migrations the database has not applied.');
  });

  it('names both kinds of drift', () => {
    const text = verdictSummary(
      status({ verdict: 'drift', counts: { applied: 4, pending: 0, failed: 0, modified: 1, missing: 2, rolled_back: 0 } }),
    );

    expect(text).toContain('1 applied migration was edited afterwards');
    expect(text).toContain('2 applied migrations are not in the running build');
  });

  it('says a failed migration blocks the rest', () => {
    const text = verdictSummary(
      status({ verdict: 'failed', counts: { applied: 4, pending: 1, failed: 1, modified: 0, missing: 0, rolled_back: 0 } }),
    );

    expect(text).toContain('1 migration started and did not finish');
  });

  it('does not claim everything is applied when the build could not be read', () => {
    const text = verdictSummary(
      status({
        shipped: { ok: false, target: '', error: 'no running container' },
        counts: { applied: 3, pending: 0, failed: 0, modified: 0, missing: 0, rolled_back: 0 },
      }),
    );

    expect(text).toContain('3 migrations are applied');
    expect(text).toContain('could not be read');
  });

  it('admits an unreadable history', () => {
    expect(verdictSummary(status({ verdict: 'unknown' }))).toContain('could not be read from the database');
  });
});

describe('canResolve', () => {
  it('allows only a failed migration', () => {
    expect(canResolve({ name: 'a', state: 'failed', steps: 0, attempts: 1 })).toBe(true);
    expect(canResolve({ name: 'a', state: 'applied', steps: 1, attempts: 1 })).toBe(false);
    expect(canResolve({ name: 'a', state: 'modified', steps: 1, attempts: 1 })).toBe(false);
    expect(canResolve({ name: 'a', state: 'pending', steps: 0, attempts: 0 })).toBe(false);
  });
});

describe('formatting', () => {
  it('formats durations by size', () => {
    expect(formatDuration(undefined)).toBe('');
    expect(formatDuration(42)).toBe('42 ms');
    expect(formatDuration(1500)).toBe('1.5 s');
    expect(formatDuration(125_000)).toBe('2 min 5 s');
  });

  it('shortens checksums', () => {
    expect(shortChecksum('0123456789abcdef0123')).toBe('0123456789ab');
    expect(shortChecksum(undefined)).toBe('');
  });
});

describe('runs', () => {
  const runs = [
    run({ id: 4, kind: 'deploy', state: 'running' }),
    run({ id: 3, kind: 'seed', actor: 'dana' }),
    run({ id: 2, kind: 'seed', actor: 'old' }),
  ];

  it('finds the newest seed', () => {
    expect(lastSeed(runs)?.actor).toBe('dana');
    expect(lastSeed([run({ kind: 'deploy' })])).toBeNull();
  });

  it('spots a run in progress', () => {
    expect(hasRunningRun(runs)).toBe(true);
    expect(hasRunningRun(runs.slice(1))).toBe(false);
  });
});

describe('parseCommand', () => {
  it('splits on whitespace into an argument vector', () => {
    expect(parseCommand('  pnpm   exec prisma ')).toEqual(['pnpm', 'exec', 'prisma']);
    expect(parseCommand('')).toEqual([]);
  });
});
