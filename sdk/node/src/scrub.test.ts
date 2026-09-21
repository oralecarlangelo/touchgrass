import { describe, expect, it } from 'vitest';
import { compileScrub, scrubReport } from './scrub.js';

describe('compileScrub', () => {
  it('compiles strings literally', () => {
    const [pattern] = compileScrub(['user@example.com']);

    expect('mail user@example.com here'.replace(pattern as RegExp, 'X')).toBe('mail X here');
    // The dot must not act as a wildcard.
    expect('mail userXexampleXcom here'.replace(pattern as RegExp, 'X')).toBe(
      'mail userXexampleXcom here',
    );
  });

  it('forces global matching on regexes', () => {
    const [pattern] = compileScrub([/secret-\d+/]);

    expect('secret-1 and secret-2'.replace(pattern as RegExp, 'X')).toBe('X and X');
  });

  it('drops invalid patterns', () => {
    expect(compileScrub([''])).toEqual([]);
    expect(compileScrub(undefined)).toEqual([]);
    expect(compileScrub(null as unknown as [])).toEqual([]);
  });
});

describe('scrubReport', () => {
  it('redacts message, crumbs, and stack paths', () => {
    const patterns = compileScrub(['hunter2']);

    const result = scrubReport(
      { message: 'login hunter2 failed' },
      [{ at: 't', category: 'auth hunter2', message: 'ok' }],
      [{ function: 'f', file: '/home/hunter2/app.js', line: 1, column: 1 }],
      patterns,
    );

    expect(result.message).toBe('login [redacted] failed');
    expect(result.breadcrumbs[0]?.category).toBe('auth [redacted]');
    expect(result.stack[0]?.file).toBe('/home/[redacted]/app.js');
  });

  it('passes reports through without patterns', () => {
    const crumbs = [{ at: 't', category: 'c', message: 'm' }];
    const frames = [{ function: 'f', file: 'a.js', line: 1, column: 1 }];

    const result = scrubReport({ message: 'plain' }, crumbs, frames, []);

    expect(result).toEqual({ message: 'plain', breadcrumbs: crumbs, stack: frames });
  });
});
