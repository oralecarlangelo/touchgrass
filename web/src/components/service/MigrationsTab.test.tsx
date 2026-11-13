import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { MigrationsView } from './MigrationsTab.tsx';
import { commandEffect, commandTitle } from '../migrations/MigrationCommandDialog.tsx';
import { formFromConfig, inputFromForm, SetupFields } from '../migrations/MigrationSetupDialog.tsx';
import type { MigrationConfigResponse, MigrationRun, MigrationStatus } from '@/lib/api.ts';

const database = 'db.internal:5432/shop (schema public)';

const status: MigrationStatus = {
  configured: true,
  tool: 'prisma',
  verdict: 'failed',
  history: { ok: true, target: database },
  shipped: { ok: true, target: 'shop-api-1:/app/prisma/migrations' },
  counts: { applied: 1, pending: 1, failed: 1, modified: 0, missing: 0, rolled_back: 0 },
  migrations: [
    {
      name: '20261101100000_init',
      state: 'applied',
      steps: 1,
      attempts: 1,
      started_at: '2026-11-01T10:00:00Z',
      finished_at: '2026-11-01T10:00:01Z',
      duration_ms: 1000,
    },
    {
      name: '20261102090000_add_colour',
      state: 'failed',
      steps: 0,
      attempts: 1,
      started_at: '2026-11-02T09:00:00Z',
      logs: 'ERROR: column "colour" already exists',
    },
    { name: '20261103120000_orders', state: 'pending', steps: 0, attempts: 0 },
  ],
  seed: { configured: true, command: 'tsx prisma/seed.ts' },
  checked_at: '2026-11-13T09:00:00Z',
};

const runs: MigrationRun[] = [
  {
    id: 2,
    service_id: 'shop-api',
    kind: 'seed',
    tool: 'prisma',
    detail: 'npx --no-install prisma db seed',
    actor: 'dana',
    state: 'succeeded',
    exit_code: 0,
    container: 'shop-api-1',
    image: 'shop/api:7',
    started_at: '2026-11-12T08:00:00Z',
  },
];

function render(props: { status: MigrationStatus; runs: MigrationRun[]; canManage: boolean }): string {
  return renderToStaticMarkup(
    <MigrationsView
      {...props}
      onRefresh={() => undefined}
      onSetup={() => undefined}
      onCommand={() => undefined}
      onOutput={() => undefined}
    />,
  );
}

describe('MigrationsView', () => {
  it('shows a developer the status, the failure and the seed history, with no controls', () => {
    const html = render({ status, runs, canManage: false });

    expect(html).toContain('Failed migration');
    expect(html).toContain('1 migration started and did not finish');
    expect(html).toContain(database);
    expect(html).toContain('20261102090000_add_colour');
    expect(html).toContain('column &quot;colour&quot; already exists');
    expect(html).toContain('tsx prisma/seed.ts');
    expect(html).toContain('by dana');

    expect(html).not.toContain('Apply pending migrations');
    expect(html).not.toContain('Run seed');
    expect(html).not.toContain('Mark applied');
    expect(html).not.toContain('Setup');
    expect(html).not.toContain('>View<');
  });

  it('shows an admin the commands, and resolve only on the failed migration', () => {
    const html = render({ status, runs, canManage: true });

    expect(html).toContain('Apply pending migrations');
    expect(html).toContain('Run seed');
    expect(html).toContain('Setup');
    expect(html).toContain('>View<');
    expect(html.match(/Mark applied/g)).toHaveLength(1);
    expect(html.match(/Mark rolled back/g)).toHaveLength(1);
  });

  it('holds the deploy button while a migration is failed, and frees it once resolved', () => {
    expect(render({ status, runs, canManage: true })).toMatch(/<button[^>]*disabled=""[^>]*>Apply pending migrations/);

    const resolved: MigrationStatus = {
      ...status,
      verdict: 'pending',
      counts: { ...status.counts, failed: 0, pending: 2 },
      migrations: status.migrations.map((migration) =>
        migration.state === 'failed' ? { ...migration, state: 'pending' as const } : migration,
      ),
    };

    expect(render({ status: resolved, runs, canManage: true })).not.toMatch(
      /<button[^>]*disabled=""[^>]*>Apply pending migrations/,
    );
  });

  it('lists the newest migration first', () => {
    const html = render({ status, runs, canManage: false });

    expect(html.indexOf('20261103120000_orders')).toBeLessThan(html.indexOf('20261101100000_init'));
  });

  it('disables the commands while one is running', () => {
    const running: MigrationRun[] = [{ ...runs[0], id: 3, kind: 'deploy', state: 'running', exit_code: undefined }];
    const html = render({ status, runs: running, canManage: true });

    expect(html).toContain('A command is running for this service');
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>Apply pending migrations/);
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>Run seed/);
  });

  it('says which side could not be read', () => {
    const html = render({
      status: {
        ...status,
        verdict: 'unknown',
        history: { ok: false, target: database, error: 'connecting to postgres: connection refused' },
      },
      runs: [],
      canManage: false,
    });

    expect(html).toContain('connection refused');
    expect(html).toContain('The migration history could not be read from the database.');
  });

  it('offers setup to an admin and explains the gap to a developer', () => {
    const empty: MigrationStatus = { ...status, configured: false, verdict: 'unknown', migrations: [] };

    expect(render({ status: empty, runs: [], canManage: true })).toContain('Set up migrations');

    const developer = render({ status: empty, runs: [], canManage: false });

    expect(developer).toContain('An admin has not set up migration tracking');
    expect(developer).not.toContain('Set up migrations');
  });

  it('does not offer a seed the build does not declare', () => {
    const html = render({ status: { ...status, seed: { configured: false } }, runs: [], canManage: true });

    expect(html).toContain('The running build declares no seed.');
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>Run seed/);
    expect(html).toContain('No seed has been run from touchgrass.');
  });
});

describe('command wording', () => {
  it('says what each command does to the database', () => {
    expect(commandTitle({ kind: 'deploy', pending: 2 })).toBe('Apply pending migrations');
    expect(commandEffect({ kind: 'deploy', pending: 2 }, database)).toContain(`Applies 2 pending migrations to ${database}`);
    expect(commandEffect({ kind: 'seed', command: 'tsx prisma/seed.ts' }, database)).toContain('not safe to repeat');
    expect(
      commandEffect({ kind: 'resolve', migration: 'm1', resolution: 'applied' }, database),
    ).toContain('without running it');
    expect(
      commandEffect({ kind: 'resolve', migration: 'm1', resolution: 'rolled_back' }, database),
    ).toContain('so it can be applied again');
  });
});

describe('setup form', () => {
  const prismaTool = {
    name: 'prisma',
    default_migrations_dir: 'prisma/migrations',
    default_command: ['npx', '--no-install', 'prisma'],
  };

  const stored: MigrationConfigResponse = {
    configured: true,
    tools: [prismaTool],
    config: {
      service_id: 'shop-api',
      tool: 'prisma',
      database,
      container: 'api',
      workdir: '/srv/app',
      migrations_dir: 'db/migrations',
      command: ['pnpm', 'exec', 'prisma'],
      updated_by: 'dana',
      updated_at: '2026-11-13T09:00:00Z',
    },
  };

  it('never prefills the database URL, and sends a blank one to keep it', () => {
    const form = formFromConfig(stored);

    expect(form.databaseUrl).toBe('');
    expect(form.command).toBe('pnpm exec prisma');
    expect(inputFromForm(form)).toEqual({
      tool: 'prisma',
      database_url: '',
      container: 'api',
      workdir: '/srv/app',
      migrations_dir: 'db/migrations',
      command: ['pnpm', 'exec', 'prisma'],
    });
  });

  it('shows where the stored URL points instead of the URL', () => {
    const html = renderToStaticMarkup(
      <SetupFields setup={stored} form={formFromConfig(stored)} busy={false} onChange={() => undefined} />,
    );

    expect(html).toContain(`Stored: ${database}`);
    expect(html).toContain('type="password"');
  });

  it("quotes the chosen tool's defaults, whatever the tool", () => {
    const ledger = { name: 'ledger', default_migrations_dir: 'db/changes', default_command: ['bin/ledger'] };
    const setup: MigrationConfigResponse = { configured: false, tools: [ledger], config: null };
    const html = renderToStaticMarkup(
      <SetupFields setup={setup} form={formFromConfig(setup)} busy={false} onChange={() => undefined} />,
    );

    expect(html).toContain('Blank uses db/changes.');
    expect(html).toContain('Blank uses bin/ledger.');
    expect(html).not.toContain('prisma');
  });

  it('starts a new setup on the first tool offered', () => {
    const form = formFromConfig({ configured: false, tools: [prismaTool], config: null });

    expect(form.tool).toBe('prisma');
    expect(inputFromForm({ ...form, databaseUrl: '  postgresql://u:p@h/db  ' }).database_url).toBe('postgresql://u:p@h/db');
  });
});
