import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import EnvEntriesTable, { type EnvEntryActions } from './EnvEntriesTable.tsx';
import { TooltipProvider } from '@/components/ui/tooltip';
import type { EnvEntry } from '@/lib/api.ts';
import { initialRevealState, type RevealState } from '@/lib/env.ts';

const noop = () => undefined;
const actions: EnvEntryActions = {
  onSave: noop,
  onRemove: noop,
  onUndo: noop,
  onToggleSecret: noop,
  onReveal: noop,
  onHideReveal: noop,
  onCopy: noop,
};


// buttonTag returns the opening tag of the button carrying this aria-label,
// so attribute order never matters to a disabled check.
function buttonTag(html: string, label: string): string {
  const match = html.match(new RegExp(`<button[^>]*aria-label="${label}"[^>]*>`));
  return match?.[0] ?? '';
}

function entry(overrides: Partial<EnvEntry> & { key: string }): EnvEntry {
  return {
    is_secret: false,
    value: 'v',
    masked: false,
    updated_by: 'ana',
    updated_at: '2026-11-10T00:00:00Z',
    state: 'applied',
    required: false,
    ...overrides,
  };
}

function render(entries: EnvEntry[], opts: { isAdmin?: boolean; readOnly?: boolean; reveal?: RevealState } = {}): string {
  return renderToStaticMarkup(
    <TooltipProvider>
      <EnvEntriesTable
        entries={entries}
        isAdmin={opts.isAdmin ?? true}
        readOnly={opts.readOnly ?? false}
        busyKey={null}
        reveal={opts.reveal ?? initialRevealState}
        now={0}
        sort="key"
        onSort={noop}
        actions={actions}
        emptyMessage="Nothing here"
      />
    </TooltipProvider>,
  );
}

describe('EnvEntriesTable', () => {
  it('renders the empty message', () => {
    expect(render([])).toContain('Nothing here');
  });

  it('keeps a masked secret out of the DOM and gives admins reveal and copy', () => {
    const html = render([entry({ key: 'TOKEN', is_secret: true, value: null, masked: true })]);
    expect(html).toContain('••••••••');
    expect(html).toContain('aria-label="Reveal TOKEN"');
    expect(html).toContain('aria-label="Copy TOKEN to clipboard"');
    expect(html).toContain('aria-label="Set a new value for TOKEN"');
    expect(html).toContain('aria-label="Mark TOKEN as plain text"');
    expect(html).not.toContain('data-revealed');
  });

  it('shows a rate-limit countdown instead of an enabled eye', () => {
    const html = render([entry({ key: 'TOKEN', is_secret: true, value: null, masked: true })], {
      reveal: { ...initialRevealState, rateLimitedUntil: 9_000 },
    });
    expect(html).toContain('aria-label="Reveal TOKEN (wait 9s)"');
    expect(html).toContain('wait 9s');
  });

  it('offers undo for added and removed rows, and explains when it cannot', () => {
    const html = render([
      entry({ key: 'ADDED', state: 'added' }),
      entry({ key: 'GONE', state: 'removed', value: 'old' }),
      entry({ key: 'ROTATED', state: 'changed', is_secret: true, value: null, masked: true }),
    ]);
    expect(html).toContain('aria-label="Undo the staged change to ADDED"');
    expect(html).toContain('aria-label="Undo the staged change to GONE"');
    expect(html).toContain('aria-label="Undo not available for ROTATED"');
    expect(html).not.toContain('aria-label="Undo the staged change to ROTATED"');
    // A removed row cannot be edited or removed again.
    expect(html).not.toContain('aria-label="Edit GONE"');
    expect(html).not.toContain('aria-label="Remove GONE"');
  });

  it('truncates long plain values with an expand control and badges required keys', () => {
    const long = 'x'.repeat(120);
    const html = render([entry({ key: 'LONG', value: long, required: true })]);
    expect(html).not.toContain(long);
    expect(html).toContain('aria-label="Expand LONG (120 characters)"');
    expect(html).toContain('>required<');
  });

  it('renders viewers a read-only table without an actions column', () => {
    const html = render([entry({ key: 'PLAIN', value: 'hello' })], { isAdmin: false });
    expect(html).toContain('hello');
    expect(html).not.toContain('aria-label="Edit PLAIN"');
    expect(html).not.toContain('aria-label="Remove PLAIN"');
    expect(html).not.toContain('>Actions<');
  });

  it('disables row actions while read-only', () => {
    const html = render([entry({ key: 'PLAIN', value: 'hello' })], { readOnly: true });
    expect(buttonTag(html, 'Edit PLAIN')).toContain('disabled=""');
  });

  it('exposes sort controls and relative times with the full timestamp on hover', () => {
    const html = render([entry({ key: 'A' })]);
    expect(html).toContain('aria-label="Sort by key"');
    expect(html).toContain('aria-label="Sort by updated"');
    expect(html).toContain('aria-sort="ascending"');
    expect(html).toContain('dateTime="2026-11-10T00:00:00Z"');
    expect(html).toContain('title="');
  });
});
