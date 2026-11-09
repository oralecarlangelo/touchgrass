import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { EnvPasteReview, acceptedLines, defaultDecisions } from './EnvPasteSheet.tsx';
import { parseDotenvLines } from '@/lib/env.ts';

const noop = () => undefined;
const text = ['# payment keys', 'GCASH_KEY=abc', 'APP_URL="https://shop.example"', '1bad=1', 'not a pair', 'GCASH_KEY=dupe', ''].join('\n');

describe('EnvPasteSheet review', () => {
  it('defaults to accepting valid lines and guessing secrets by name', () => {
    const lines = parseDotenvLines(text);
    const decisions = defaultDecisions(lines);

    expect(decisions[2]).toEqual({ accept: true, secret: true });
    expect(decisions[3]).toEqual({ accept: true, secret: false });
    expect(decisions[4]).toEqual({ accept: false, secret: false });
    expect(decisions[5]).toEqual({ accept: false, secret: false });
    expect(decisions[6]).toEqual({ accept: false, secret: true });
    expect(acceptedLines(lines, decisions)).toEqual([
      { key: 'GCASH_KEY', value: 'abc', isSecret: true },
      { key: 'APP_URL', value: 'https://shop.example', isSecret: false },
    ]);
  });

  it('honours per-line skip and secret overrides', () => {
    const lines = parseDotenvLines(text);
    const decisions = { ...defaultDecisions(lines), 2: { accept: false, secret: true }, 3: { accept: true, secret: true } };

    expect(acceptedLines(lines, decisions)).toEqual([{ key: 'APP_URL', value: 'https://shop.example', isSecret: true }]);
  });

  it('renders each line with its status, masks guessed secrets, and flags replacements', () => {
    const lines = parseDotenvLines(text);
    const html = renderToStaticMarkup(
      <EnvPasteReview lines={lines} decisions={defaultDecisions(lines)} existingKeys={new Set(['APP_URL'])} busy={false} onDecision={noop} />,
    );
    expect(html).toContain('data-line-status="ok"');
    expect(html).toContain('data-line-status="invalid_key"');
    expect(html).toContain('data-line-status="unparsable"');
    expect(html).toContain('data-line-status="duplicate"');
    expect(html).toContain('••••••••');
    expect(html).not.toContain('>abc<');
    expect(html).toContain('https://shop.example');
    expect(html).toContain('Replaces the current APP_URL.');
    expect(html).toContain('Same key as line 2');
    expect(html).toContain('aria-label="Treat GCASH_KEY as secret"');
  });

  it('explains an empty paste', () => {
    expect(renderToStaticMarkup(<EnvPasteReview lines={[]} decisions={{}} existingKeys={new Set()} busy={false} onDecision={noop} />)).toContain(
      'Nothing to stage',
    );
  });
});
