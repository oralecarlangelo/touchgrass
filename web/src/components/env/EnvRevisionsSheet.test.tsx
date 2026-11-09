import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { EnvRevisionsList, sortRevisions } from './EnvRevisionsSheet.tsx';
import type { EnvRevision } from '@/lib/api.ts';

const noop = () => undefined;

// buttonTag returns the opening tag of the button carrying this aria-label,
// so attribute order never matters to a disabled check.
function buttonTag(html: string, label: string): string {
  const match = html.match(new RegExp(`<button[^>]*aria-label="${label}"[^>]*>`));
  return match?.[0] ?? '';
}

const revisions: EnvRevision[] = [
  { id: 'rev-1', created_at: '2026-11-08T00:00:00Z', created_by: 'ana', summary: 'Initial import', added: ['APP_NAME', 'DB_URL', 'PORT'], changed: [], removed: [], rendered_sha256: 'sha256:aaaaaaaa1111', applied: false },
  { id: 'rev-2', created_at: '2026-11-09T00:00:00Z', created_by: 'ben', summary: 'Rotate GCASH_KEY', added: [], changed: ['GCASH_KEY'], removed: [], rendered_sha256: 'sha256:bbbbbbbb2222', applied: true },
];

function render(overrides: Partial<Parameters<typeof EnvRevisionsList>[0]> = {}): string {
  return renderToStaticMarkup(
    <EnvRevisionsList
      revisions={revisions}
      error={null}
      serviceName="Shop API"
      isAdmin
      disabled={false}
      busy={false}
      rollbackTarget={null}
      confirm=""
      rollbackError={null}
      onRetry={noop}
      onPickRollback={noop}
      onConfirmChange={noop}
      onRollback={noop}
      {...overrides}
    />,
  );
}

describe('EnvRevisionsList', () => {
  it('renders loading, error and empty states', () => {
    expect(render({ revisions: null })).toContain('Loading revisions');
    expect(render({ revisions: null, error: 'offline' })).toContain('load revisions');
    const empty = render({ revisions: [] });
    expect(empty).toContain('No revisions yet');
    expect(empty).toContain('Every apply saves one');
  });

  it('lists newest first with the current badge, counts and short sha', () => {
    expect(sortRevisions(revisions).map((revision) => revision.id)).toEqual(['rev-2', 'rev-1']);
    const html = render();
    expect(html.indexOf('Rotate GCASH_KEY')).toBeLessThan(html.indexOf('Initial import'));
    expect(html).toContain('>current<');
    expect(html).toContain('bbbbbbbb');
    expect(html).toContain('+3 added');
    expect(html).toContain('~1 changed');
    expect(html).toContain('ben');
    // The applied revision has no rollback button; the older one does.
    expect((html.match(/Roll back to this/g) ?? []).length).toBe(1);
  });

  it('hides rollback from viewers', () => {
    expect(render({ isAdmin: false })).not.toContain('Roll back');
  });

  it('keeps the rollback button disabled until the name matches', () => {
    const wrong = render({ rollbackTarget: 'rev-1', confirm: 'shop' });
    const right = render({ rollbackTarget: 'rev-1', confirm: 'Shop API' });
    expect(wrong).toContain('Type <span');
    expect(buttonTag(wrong, 'Roll back to revision rev-1')).toContain('disabled=""');
    expect(buttonTag(right, 'Roll back to revision rev-1')).not.toContain('disabled=""');
    expect(render({ rollbackTarget: 'rev-1', confirm: 'Shop API', rollbackError: 'confirmation did not match' })).toContain(
      'confirmation did not match',
    );
  });
});
