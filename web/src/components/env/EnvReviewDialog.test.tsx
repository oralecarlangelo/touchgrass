import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { EnvReviewBody, EnvReviewFooter, confirmationMatches } from './EnvReviewDialog.tsx';

const noop = () => undefined;

// buttonTag returns the opening tag of the button carrying this aria-label,
// so attribute order never matters to a disabled check.
function buttonTag(html: string, label: string): string {
  const match = html.match(new RegExp(`<button[^>]*aria-label="${label}"[^>]*>`));
  return match?.[0] ?? '';
}

const diff = { added: ['SMTP_HOST'], changed: ['GCASH_KEY'], removed: ['OLD_FLAG'], rendered_sha256: 'sha256:0123456789abcdef' };

describe('EnvReviewDialog', () => {
  it('gates on the exact service name', () => {
    expect(confirmationMatches('Shop API', 'Shop API')).toBe(true);
    expect(confirmationMatches('  Shop API ', 'Shop API')).toBe(true);
    expect(confirmationMatches('shop api', 'Shop API')).toBe(false);
    expect(confirmationMatches('', '')).toBe(false);
  });

  it('keeps Apply disabled until the name matches', () => {
    const locked = renderToStaticMarkup(<EnvReviewFooter ready={false} busy={false} onClose={noop} onSubmit={noop} />);
    const open = renderToStaticMarkup(<EnvReviewFooter ready busy={false} onClose={noop} onSubmit={noop} />);
    expect(buttonTag(locked, 'Apply environment changes')).toContain('disabled=""');
    expect(buttonTag(open, 'Apply environment changes')).not.toContain('disabled=""');
    expect(renderToStaticMarkup(<EnvReviewFooter ready={false} busy onClose={noop} onSubmit={noop} />)).toContain('Starting…');
  });

  it('shows the diff as three lists, the steps, the sha and the gate copy', () => {
    const html = renderToStaticMarkup(
      <EnvReviewBody
        serviceName="Shop API"
        diff={diff}
        diffError={null}
        summary="Rotate GCASH_KEY; Add SMTP_HOST; Remove OLD_FLAG"
        confirm=""
        onSummaryChange={noop}
        onConfirmChange={noop}
        onSubmitAttempt={noop}
        busy={false}
        error={null}
      />,
    );
    expect(html).toContain('aria-label="Added keys"');
    expect(html).toContain('aria-label="Changed keys"');
    expect(html).toContain('aria-label="Removed keys"');
    expect(html).toContain('SMTP_HOST');
    expect(html).toContain('GCASH_KEY');
    expect(html).toContain('OLD_FLAG');
    expect(html).toContain('01234567');
    for (const step of ['Render', 'Validate', 'Write', 'Restart', 'Probe']) {
      expect(html).toContain(step);
    }
    expect(html).toContain('value="Rotate GCASH_KEY; Add SMTP_HOST; Remove OLD_FLAG"');
    expect(html).toContain('to confirm');
    expect(html).toContain('Shop API');
  });

  it('shows a diff loading skeleton, a diff error and a server error', () => {
    const base = {
      serviceName: 'Shop API',
      summary: '',
      confirm: '',
      onSummaryChange: noop,
      onConfirmChange: noop,
      onSubmitAttempt: noop,
      busy: false,
    };
    expect(renderToStaticMarkup(<EnvReviewBody {...base} diff={null} diffError={null} error={null} />)).toContain('Loading diff');
    expect(renderToStaticMarkup(<EnvReviewBody {...base} diff={null} diffError="offline" error={null} />)).toContain('load the diff');
    expect(renderToStaticMarkup(<EnvReviewBody {...base} diff={diff} diffError={null} error="confirmation did not match" />)).toContain(
      'confirmation did not match',
    );
  });
});
