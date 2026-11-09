import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import EnvApplyProgress from './EnvApplyProgress.tsx';
import { initialRunState, reduceRunEvent, type EnvApplyEvent } from '@/lib/env.ts';

const noop = () => undefined;

function play(events: [EnvApplyEvent['step'], string][]) {
  let state = initialRunState('run-1', '2026-11-10T00:00:00Z');
  events.forEach(([step, message], index) => {
    state = reduceRunEvent(state, {
      run_id: 'run-1',
      service_id: 'svc',
      step,
      message,
      at: `2026-11-10T00:00:${String(index + 1).padStart(2, '0')}Z`,
    });
  });
  return state;
}

describe('EnvApplyProgress', () => {
  it('renders a running stepper fed by SSE events with elapsed time', () => {
    const run = play([['render', 'rendered 12 keys'], ['validate', 'running validator']]);
    const html = renderToStaticMarkup(
      <EnvApplyProgress run={run} kind="apply" now={Date.parse('2026-11-10T00:00:42Z')} streamLost={false} onRetry={null} onDismiss={noop} />,
    );
    expect(html).toContain('Apply running');
    expect(html).toContain('data-step="render" data-status="done"');
    expect(html).toContain('data-step="validate" data-status="running"');
    expect(html).toContain('data-step="write" data-status="pending"');
    expect(html).toContain('rendered 12 keys');
    expect(html).toContain('42s');
    expect(html).not.toContain('Retry');
  });

  it('notes the poll fallback when the stream drops', () => {
    const run = play([['render', '']]);
    const html = renderToStaticMarkup(<EnvApplyProgress run={run} kind="apply" now={0} streamLost onRetry={null} onDismiss={noop} />);
    expect(html).toContain('polling instead');
  });

  it('highlights the failing step and shows validator lines verbatim', () => {
    const run = play([
      ['render', 'ok'],
      ['validate', 'running validator'],
      ['failed', 'validator: DB_URL must be a URL\nvalidator: missing SMTP_HOST'],
    ]);
    const html = renderToStaticMarkup(
      <EnvApplyProgress run={run} kind="apply" now={0} streamLost={false} onRetry={noop} onDismiss={noop} />,
    );
    expect(html).toContain('Apply failed');
    expect(html).toContain('Validate failed');
    expect(html).toContain('data-step="validate" data-status="failed"');
    expect(html).toContain('aria-label="Validator output"');
    expect(html).toContain('validator: DB_URL must be a URL\nvalidator: missing SMTP_HOST');
    expect(html).toContain('Nothing was written to the host.');
    expect(html).toContain('Your changes stay staged');
    expect(html).toContain('Retry');
    expect(html).toContain('Dismiss');
  });

  it('says the previous file was restored when the probe fails', () => {
    const run = play([['render', ''], ['validate', ''], ['write', ''], ['restart', ''], ['probe', ''], ['failed', 'probe: 503']]);
    const html = renderToStaticMarkup(
      <EnvApplyProgress run={run} kind="rollback" now={0} streamLost={false} onRetry={null} onDismiss={noop} />,
    );
    expect(html).toContain('Rollback failed');
    expect(html).toContain('Probe failed');
    expect(html).toContain('The previous file was restored');
    expect(html).not.toContain('>Retry<');
  });

  it('renders success with every step done', () => {
    const run = play([['render', ''], ['validate', ''], ['write', ''], ['restart', ''], ['probe', ''], ['done', 'applied']]);
    const html = renderToStaticMarkup(
      <EnvApplyProgress run={run} kind="apply" now={0} streamLost={false} onRetry={null} onDismiss={noop} />,
    );
    expect(html).toContain('Apply finished');
    expect(html).toContain('data-run-status="done"');
    expect((html.match(/data-status="done"/g) ?? []).length).toBe(5);
  });
});
