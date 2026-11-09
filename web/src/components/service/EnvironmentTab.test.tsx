import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { EnvironmentTabView, type EnvironmentTabViewProps } from './EnvironmentTab.tsx';
import { TooltipProvider } from '@/components/ui/tooltip';
import type { EnvEntry, EnvOverview, ServiceView } from '@/lib/api.ts';
import { defaultEnvFilters, initialRevealState, initialRunState, reduceRunEvent } from '@/lib/env.ts';

const service: ServiceView = {
  id: 'shop-api',
  name: 'Shop API',
  strategy: 'recreate',
  live_color: '',
  health: 'healthy',
  colors: [],
  containers: [],
};

const secretValue = 'sk_live_SHOULD_NEVER_RENDER';

const entries: EnvEntry[] = [
  { key: 'DATABASE_URL', is_secret: false, value: 'postgres://db/shop', masked: false, updated_by: 'ana', updated_at: '2026-11-10T00:00:00Z', state: 'applied', required: true },
  { key: 'GCASH_KEY', is_secret: true, value: null, masked: true, updated_by: 'ana', updated_at: '2026-11-10T00:00:00Z', state: 'changed', required: false },
  { key: 'NEW_FLAG', is_secret: false, value: 'on', masked: false, updated_by: 'ben', updated_at: '2026-11-10T01:00:00Z', state: 'added', required: false },
  { key: 'OLD_FLAG', is_secret: false, value: 'off', masked: false, updated_by: 'ben', updated_at: '2026-11-10T01:00:00Z', state: 'removed', required: false },
];

const configured: EnvOverview = {
  configured: true,
  target: { path: '/srv/shop-api/.env', owner: 'app:app', mode: '0600' },
  entries,
  applied_revision: { id: 'rev-1', created_at: '2026-11-09T00:00:00Z', created_by: 'ana', summary: 'Initial import' },
  pending_count: 3,
  last_validation: { ok: false, lines: ['missing required keys: SMTP_HOST'], at: '2026-11-09T00:00:00Z' },
  apply_in_progress: false,
};

const noop = () => undefined;

function render(overrides: Partial<EnvironmentTabViewProps>): string {
  const props: EnvironmentTabViewProps = {
    service,
    isAdmin: true,
    overview: configured,
    loading: false,
    error: null,
    run: null,
    runKind: 'apply',
    streamLost: false,
    now: Date.parse('2026-11-10T02:00:00Z'),
    filters: defaultEnvFilters,
    sort: 'key',
    busyKey: null,
    addBusy: false,
    addError: null,
    prefillKey: null,
    importBusy: false,
    importError: null,
    importResult: null,
    reveal: initialRevealState,
    viewerNotice: false,
    onRetry: noop,
    onFilters: noop,
    onSort: noop,
    onStage: noop,
    onPaste: noop,
    onAddKey: noop,
    onImport: noop,
    onOpenRevisions: noop,
    onReview: noop,
    onDiscard: noop,
    onRetryRun: noop,
    onDismissRun: noop,
    entryActions: {
      onSave: noop,
      onRemove: noop,
      onUndo: noop,
      onToggleSecret: noop,
      onReveal: noop,
      onHideReveal: noop,
      onCopy: noop,
    },
    ...overrides,
  };

  return renderToStaticMarkup(
    <TooltipProvider>
      <EnvironmentTabView {...props} />
    </TooltipProvider>,
  );
}

describe('EnvironmentTabView states', () => {
  it('renders loading and error states', () => {
    expect(render({ overview: null, loading: true })).toContain('Loading environment');
    const failed = render({ overview: null, error: 'API unreachable: offline' });
    expect(failed).toContain('load the environment');
    expect(failed).toContain('offline');
    expect(failed).toContain('Retry');
  });

  it('explains a missing target with the config snippet for admins only', () => {
    const base: EnvOverview = { ...configured, configured: false, reason: 'no_target', target: null, entries: [] };
    const admin = render({ overview: base });
    const viewer = render({ overview: base, isAdmin: false });

    expect(admin).toContain('Configure the env target');
    expect(admin).toContain('env_path');
    expect(admin).toContain('/srv/shop-api/.env');
    expect(admin).toContain('/docs/env.html');
    expect(viewer).toContain('Configure the env target');
    expect(viewer).not.toContain('env_path');
    expect(viewer).toContain('An admin adds the target');
  });

  it('explains a missing encryption key', () => {
    const html = render({ overview: { ...configured, configured: false, reason: 'key_missing', target: null, entries: [] } });
    expect(html).toContain('no encryption key');
    expect(html).toContain('TOUCHGRASS_ENV_KEY');
  });

  it('offers import when configured with zero entries, admins only', () => {
    const empty: EnvOverview = { ...configured, entries: [], pending_count: 0, last_validation: null };
    const admin = render({ overview: empty });
    const viewer = render({ overview: empty, isAdmin: false });

    expect(admin).toContain('Import the current file');
    expect(admin).toContain('/srv/shop-api/.env');
    expect(viewer).toContain('No entries yet');
    expect(viewer).not.toContain('Import the current file');

    const done = render({ overview: empty, importResult: { imported: 12, skipped: 1, secret_guessed: 3 } });
    expect(done).toContain('Imported 12 entries, skipped 1; 3 marked secret by name.');
  });

  it('never renders a masked secret value and hides the eye from viewers', () => {
    const admin = render({});
    const viewer = render({ isAdmin: false });

    for (const html of [admin, viewer]) {
      expect(html).toContain('GCASH_KEY');
      expect(html).toContain('••••••••');
      expect(html).not.toContain(secretValue);
      expect(html).not.toContain('data-revealed');
    }
    expect(admin).toContain('aria-label="Reveal GCASH_KEY"');
    expect(admin).toContain('aria-label="Copy GCASH_KEY to clipboard"');
    expect(viewer).not.toContain('Reveal GCASH_KEY');
    expect(viewer).not.toContain('Copy GCASH_KEY');
    expect(viewer).not.toContain('aria-label="Edit DATABASE_URL"');
    expect(admin).toContain('aria-label="Edit DATABASE_URL"');
  });

  it('shows a revealed secret only while the reveal is live, with its countdown', () => {
    const now = 10_000;
    const html = render({
      now,
      reveal: { ...initialRevealState, key: 'GCASH_KEY', value: secretValue, expiresAt: now + 12_000 },
    });

    expect(html).toContain(secretValue);
    expect(html).toContain('data-revealed="true"');
    expect(html).toContain('12s');
    expect(html).toContain('aria-label="Hide GCASH_KEY (12s left)"');
  });

  it('shows the pending bar with counts and the state chips', () => {
    const admin = render({});
    const viewer = render({ isAdmin: false });

    expect(admin).toContain('3 changes not applied');
    expect(admin).toContain('1 added');
    expect(admin).toContain('1 changed');
    expect(admin).toContain('1 removed');
    expect(admin).toContain('Review &amp; apply');
    expect(admin).toContain('Discard');
    expect(admin).toContain('data-state="removed"');
    expect(admin).toContain('line-through');
    expect(viewer).toContain('3 changes not applied');
    expect(viewer).not.toContain('Review &amp; apply');
    expect(viewer).toContain('An admin applies these.');
  });

  it('shows the missing-required callout with Add shortcuts and the required badge', () => {
    const html = render({});
    expect(html).toContain('1 required key is missing');
    expect(html).toContain('SMTP_HOST');
    expect(html).toContain('aria-label="Add SMTP_HOST"');
    expect(html).toContain('>required<');
    expect(html).toContain('Validation failed');
  });

  it('goes read-only with the progress pinned while an apply runs', () => {
    let run = initialRunState('run-9', '2026-11-10T01:59:00Z');
    run = reduceRunEvent(run, { run_id: 'run-9', service_id: 'shop-api', step: 'validate', message: 'checking', at: '2026-11-10T01:59:30Z' });
    const html = render({ run, overview: { ...configured, apply_in_progress: true } });

    expect(html).toContain('Apply running');
    expect(html).toContain('data-step="validate" data-status="running"');
    expect(html).toContain('1m 0s');
    // Add row and row actions are disabled; the pending bar buttons too.
    expect(html).toContain('aria-label="Stage this entry"');
    expect(html.match(/disabled=""/g)?.length ?? 0).toBeGreaterThan(5);
    expect(html.indexOf('Apply running')).toBeLessThan(html.indexOf('DATABASE_URL'));
  });

  it('tells the operator about a run started elsewhere when the API gives no run id', () => {
    const html = render({ overview: { ...configured, apply_in_progress: true } });
    expect(html).toContain('An apply is running');
    expect(html).toContain('Another session started it');
  });

  it('shows the viewer notice after a 403', () => {
    expect(render({ isAdmin: false, viewerNotice: true })).toContain('Read-only account');
  });

  it('keeps a refresh error inline when data is already on screen', () => {
    const html = render({ error: 'API unreachable: timeout' });
    expect(html).toContain('refresh</div>');
    expect(html).toContain('DATABASE_URL');
  });

  it('filters, searches and reports the visible count', () => {
    const pending = render({ filters: { ...defaultEnvFilters, pendingOnly: true } });
    expect(pending).not.toContain('data-key="DATABASE_URL"');
    expect(pending).toContain('3 of 4 entries');

    const search = render({ filters: { ...defaultEnvFilters, query: 'flag' } });
    expect(search).toContain('NEW_FLAG');
    expect(search).toContain('OLD_FLAG');
    expect(search).not.toContain('data-key="GCASH_KEY"');

    const none = render({ filters: { ...defaultEnvFilters, query: 'zzz' } });
    expect(none).toContain('No keys match these filters.');
    expect(none).toContain('Clear');
  });

  it('collapses to cards: both the table and the card list are in the markup', () => {
    const html = render({});
    expect(html).toContain('hidden lg:block');
    expect(html).toContain('lg:hidden');
    expect(html).toContain('<table');
    expect(html).toContain('<ul');
  });

  it('includes the How this works disclosure and the docs link', () => {
    const html = render({});
    expect(html).toContain('How this works');
    expect(html).toContain('/srv/shop-api/.env');
    expect(html).toContain('href="/docs/env.html"');
  });
});
