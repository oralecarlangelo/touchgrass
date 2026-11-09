import { describe, expect, it } from 'vitest';

import type { EnvEntry, EnvRun } from './api.ts';
import {
  describePending,
  elapsedSeconds,
  envShortcut,
  filterEnvEntries,
  initialRevealState,
  initialRunState,
  looksLikeDotenvPaste,
  looksSecret,
  mergeRun,
  missingRequiredKeys,
  parseDotenvLines,
  parseEnvApplyEvent,
  pendingCounts,
  reduceRunEvent,
  revealReducer,
  revealSeconds,
  secondsLeft,
  shortSha,
  sortEnvEntries,
  suggestSummary,
  truncateValue,
  undoPlan,
  validateEnvKey,
  validateEnvValue,
  type ApplyRunState,
  type EnvApplyEvent,
} from './env.ts';

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

describe('env key and value validation', () => {
  it('accepts the server pattern and rejects everything else with a human message', () => {
    expect(validateEnvKey('DATABASE_URL')).toBeNull();
    expect(validateEnvKey('_PRIVATE')).toBeNull();
    expect(validateEnvKey('A1')).toBeNull();
    expect(validateEnvKey('')).toMatch(/Enter a key/);
    expect(validateEnvKey('1ABC')).toMatch(/not starting with a digit/);
    expect(validateEnvKey('database_url')).toMatch(/upper-case/);
    expect(validateEnvKey('FOO-BAR')).toMatch(/letters, digits and underscores/);
    expect(validateEnvKey('A'.repeat(300))).toMatch(/256/);
  });

  it('rejects newlines and oversize values', () => {
    expect(validateEnvValue('')).toBeNull();
    expect(validateEnvValue('postgres://x:y@h/db?sslmode=require')).toBeNull();
    expect(validateEnvValue('a\nb')).toMatch(/single line/);
    expect(validateEnvValue('a\r\nb')).toMatch(/single line/);
    expect(validateEnvValue('x'.repeat(40_000))).toMatch(/stop at/);
  });
});

describe('looksSecret', () => {
  it('flags secret-looking names and leaves public ones alone', () => {
    for (const key of ['GCASH_KEY', 'API_SECRET', 'DB_PASSWORD', 'JWT_SIGNING_KEY', 'SENTRY_DSN', 'ACCESS_TOKEN', 'AUTH_SALT']) {
      expect(looksSecret(key), key).toBe(true);
    }
    for (const key of ['DATABASE_URL', 'PUBLIC_KEY', 'STRIPE_PUBLISHABLE_KEY', 'REDIS_HOST', 'PORT', 'APP_NAME', 'KEY_ID']) {
      expect(looksSecret(key), key).toBe(false);
    }
  });
});

describe('parseDotenvLines', () => {
  it('handles comments, blanks, export, quotes, inline comments and secret guessing', () => {
    const text = [
      '# payment keys',
      '',
      'export GCASH_KEY="abc\\"123"',
      "PAYMONGO_SECRET='s3cr#t'",
      'APP_URL=https://shop.example # prod',
      'EMPTY=',
      'MULTI="line one\\nline two"',
    ].join('\n');
    const lines = parseDotenvLines(text);

    expect(lines.map((line) => line.key)).toEqual(['GCASH_KEY', 'PAYMONGO_SECRET', 'APP_URL', 'EMPTY', 'MULTI']);
    expect(lines[0]).toMatchObject({ line: 3, value: 'abc"123', status: 'ok', secret: true });
    expect(lines[1]).toMatchObject({ value: 's3cr#t', status: 'ok', secret: true });
    expect(lines[2]).toMatchObject({ value: 'https://shop.example', status: 'ok', secret: false });
    expect(lines[3]).toMatchObject({ value: '', status: 'ok' });
    // A double-quoted \n is a real newline, which the server refuses.
    expect(lines[4]).toMatchObject({ status: 'invalid_value' });
  });

  it('marks invalid keys, duplicates and unparsable lines without dropping them', () => {
    const lines = parseDotenvLines('lower=1\nGOOD=1\nGOOD=2\n1BAD=x\njust words\n');

    expect(lines.map((line) => line.status)).toEqual(['invalid_key', 'ok', 'duplicate', 'invalid_key', 'unparsable']);
    expect(lines[2].message).toMatch(/line 2/);
    expect(lines[4].message).toMatch(/KEY=VALUE/);
  });

  it('recognises a .env paste versus a plain key', () => {
    expect(looksLikeDotenvPaste('FOO=bar')).toBe(true);
    expect(looksLikeDotenvPaste('# c\nexport FOO=bar\nBAZ=1')).toBe(true);
    expect(looksLikeDotenvPaste('DATABASE_URL')).toBe(false);
    expect(looksLikeDotenvPaste('')).toBe(false);
  });
});

describe('filtering and sorting', () => {
  const entries = [
    entry({ key: 'B_KEY', is_secret: true, state: 'changed', updated_at: '2026-11-10T02:00:00Z' }),
    entry({ key: 'A_URL', required: true, updated_at: '2026-11-10T01:00:00Z' }),
    entry({ key: 'C_FLAG', state: 'removed', updated_at: '2026-11-10T03:00:00Z' }),
  ];

  it('filters by key substring and the three toggles', () => {
    const base = { query: '', pendingOnly: false, secretsOnly: false, requiredOnly: false };

    expect(filterEnvEntries(entries, { ...base, query: 'url' }).map((e) => e.key)).toEqual(['A_URL']);
    expect(filterEnvEntries(entries, { ...base, pendingOnly: true }).map((e) => e.key)).toEqual(['B_KEY', 'C_FLAG']);
    expect(filterEnvEntries(entries, { ...base, secretsOnly: true }).map((e) => e.key)).toEqual(['B_KEY']);
    expect(filterEnvEntries(entries, { ...base, requiredOnly: true }).map((e) => e.key)).toEqual(['A_URL']);
    expect(filterEnvEntries(entries, { ...base, pendingOnly: true, secretsOnly: true }).map((e) => e.key)).toEqual(['B_KEY']);
  });

  it('sorts by key or by most recent update', () => {
    expect(sortEnvEntries(entries, 'key').map((e) => e.key)).toEqual(['A_URL', 'B_KEY', 'C_FLAG']);
    expect(sortEnvEntries(entries, 'updated').map((e) => e.key)).toEqual(['C_FLAG', 'B_KEY', 'A_URL']);
  });

  it('counts and describes pending changes', () => {
    const counts = pendingCounts(entries);

    expect(counts).toEqual({ added: 0, changed: 1, removed: 1, total: 2 });
    expect(describePending(counts)).toBe('2 changes not applied · 1 changed · 1 removed');
    expect(describePending({ added: 1, changed: 0, removed: 0, total: 1 })).toBe('1 change not applied · 1 added');
  });
});

describe('suggestSummary', () => {
  it('writes an operator-style summary from the diff', () => {
    expect(suggestSummary({ added: [], changed: ['GCASH_KEY'], removed: [], rendered_sha256: '' })).toBe('Rotate GCASH_KEY');
    expect(
      suggestSummary({ added: ['SMTP_HOST', 'SMTP_PORT'], changed: ['APP_URL'], removed: ['OLD_FLAG'], rendered_sha256: '' }),
    ).toBe('Update APP_URL; Add SMTP_HOST and SMTP_PORT; Remove OLD_FLAG');
    expect(
      suggestSummary({ added: ['A', 'B', 'C', 'D', 'E'], changed: [], removed: [], rendered_sha256: '' }),
    ).toBe('Add A, B, C and 2 more');
    // A key the server marks secret counts as a rotation even if the name is bland.
    expect(
      suggestSummary({ added: [], changed: ['PEPPER'], removed: [], rendered_sha256: '' }, [entry({ key: 'PEPPER', is_secret: true })]),
    ).toBe('Rotate PEPPER');
  });
});

describe('undoPlan', () => {
  it('deletes an added row, restores a removed plain row, and explains when it cannot', () => {
    expect(undoPlan(entry({ key: 'A' }))).toBeNull();
    expect(undoPlan(entry({ key: 'A', state: 'added' }))).toEqual({ kind: 'delete' });
    expect(undoPlan(entry({ key: 'A', state: 'removed', value: 'old' }))).toEqual({ kind: 'restore', value: 'old', is_secret: false });
    expect(undoPlan(entry({ key: 'A', state: 'removed', value: null, is_secret: true, masked: true }))).toMatchObject({ kind: 'unavailable' });
    expect(undoPlan(entry({ key: 'A', state: 'changed', value: 'new', applied_value: 'old' }))).toEqual({ kind: 'restore', value: 'old', is_secret: false });
    expect(undoPlan(entry({ key: 'A', state: 'changed', value: 'new' }))).toMatchObject({ kind: 'unavailable', reason: expect.stringMatching(/Discard all changes/) });
  });
});

describe('missingRequiredKeys', () => {
  it('prefers the structured list, parses lines otherwise, and ignores present keys', () => {
    const present = [entry({ key: 'DB_URL' })];

    expect(missingRequiredKeys(null, present)).toEqual([]);
    expect(missingRequiredKeys({ ok: false, lines: [], at: '', missing_required: ['DB_URL', 'SMTP_HOST'] }, present)).toEqual(['SMTP_HOST']);
    expect(
      missingRequiredKeys({ ok: false, lines: ['error: missing required keys: SMTP_HOST, SMTP_PORT', 'note: fine'], at: '' }, present),
    ).toEqual(['SMTP_HOST', 'SMTP_PORT']);
    expect(missingRequiredKeys({ ok: true, lines: ['missing key FOO'], at: '' }, present)).toEqual([]);
  });
});

function event(step: EnvApplyEvent['step'], message = '', at = '2026-11-10T00:00:01Z'): EnvApplyEvent {
  return { run_id: 'run-1', service_id: 'svc', step, message, at };
}

describe('apply run state machine', () => {
  it('walks the stepper to done on a successful sequence', () => {
    let state = initialRunState('run-1', '2026-11-10T00:00:00Z');

    for (const step of ['render', 'validate', 'write', 'restart', 'probe'] as const) {
      state = reduceRunEvent(state, event(step, `${step} ok`));
      expect(state.steps[step]).toBe('running');
    }
    expect(state.steps.render).toBe('done');
    state = reduceRunEvent(state, event('done', 'applied', '2026-11-10T00:00:09Z'));

    expect(state.status).toBe('done');
    expect(Object.values(state.steps).every((status) => status === 'done')).toBe(true);
    expect(state.finishedAt).toBe('2026-11-10T00:00:09Z');
    expect(state.lines).toHaveLength(6);
    expect(elapsedSeconds(state.startedAt, state.finishedAt, 0)).toBe(9);
  });

  it('pins a failure on the running step and keeps validator lines verbatim', () => {
    let state = initialRunState('run-1');
    state = reduceRunEvent(state, event('render'));
    state = reduceRunEvent(state, event('validate'));
    state = reduceRunEvent(state, event('failed', 'validator: DB_URL must be a URL\nvalidator: missing SMTP_HOST'));

    expect(state.status).toBe('failed');
    expect(state.failedStep).toBe('validate');
    expect(state.steps).toMatchObject({ render: 'done', validate: 'failed', write: 'pending' });
    expect(state.validatorLines).toEqual(['validator: DB_URL must be a URL', 'validator: missing SMTP_HOST']);
    expect(state.restored).toBe(false);
    // Nothing after a terminal event changes the state.
    expect(reduceRunEvent(state, event('done'))).toBe(state);
  });

  it('notes the restore when a later step fails and ignores other runs', () => {
    let state = initialRunState('run-1');
    for (const step of ['render', 'validate', 'write', 'restart', 'probe'] as const) {
      state = reduceRunEvent(state, event(step));
    }
    state = reduceRunEvent(state, event('failed', 'probe: 503 from /health'));

    expect(state.failedStep).toBe('probe');
    expect(state.restored).toBe(true);
    expect(state.error).toBe('probe: 503 from /health');
    expect(reduceRunEvent(initialRunState('run-1'), { ...event('render'), run_id: 'other' })).toEqual(initialRunState('run-1'));
  });

  it('merges a polled run without regressing a finished state', () => {
    const run: EnvRun = {
      id: 'run-1',
      service_id: 'svc',
      status: 'failed',
      started_at: '2026-11-10T00:00:00Z',
      finished_at: '2026-11-10T00:00:05Z',
      steps: [
        { name: 'render', status: 'done', message: 'rendered 12 keys', at: '2026-11-10T00:00:01Z' },
        { name: 'validate', status: 'failed', message: 'validator exited 1', at: '2026-11-10T00:00:02Z' },
      ],
      error: 'validator exited 1',
      validation_lines: ['missing SMTP_HOST'],
    };
    const merged = mergeRun(initialRunState('run-1'), run);

    expect(merged.status).toBe('failed');
    expect(merged.failedStep).toBe('validate');
    expect(merged.validatorLines).toEqual(['missing SMTP_HOST']);
    expect(merged.lines.map((line) => line.message)).toEqual(['rendered 12 keys', 'validator exited 1']);
    expect(merged.finishedAt).toBe('2026-11-10T00:00:05Z');

    const done: ApplyRunState = { ...initialRunState('run-1'), status: 'done' };
    expect(mergeRun(done, { ...run, status: 'running' }).status).toBe('done');
    expect(mergeRun(done, { ...run, id: 'other' })).toBe(done);
  });

  it('parses env.apply events in both wire shapes and rejects others', () => {
    const named = parseEnvApplyEvent(JSON.stringify({ run_id: 'r', service_id: 's', step: 'write', message: 'm', at: 't' }));
    const typed = parseEnvApplyEvent(JSON.stringify({ type: 'env.apply', run_id: 'r', service_id: 's', step: 'done', message: '', at: 't' }));

    expect(named).toMatchObject({ step: 'write', message: 'm' });
    expect(typed).toMatchObject({ step: 'done' });
    expect(parseEnvApplyEvent(JSON.stringify({ type: 'deploy_finished', service_id: 's', message: '', at: 't' }))).toBeNull();
    expect(parseEnvApplyEvent(JSON.stringify({ run_id: 'r', service_id: 's', step: 'teleport' }))).toBeNull();
    expect(parseEnvApplyEvent('not json')).toBeNull();
  });
});

describe('reveal state machine', () => {
  it('reveals for 20 seconds, re-masks on tick, and honours rate limits', () => {
    let state = revealReducer(initialRevealState, { type: 'start', key: 'K' });
    expect(state.busyKey).toBe('K');

    state = revealReducer(state, { type: 'revealed', key: 'K', value: 'shh', now: 1_000 });
    expect(state).toMatchObject({ key: 'K', value: 'shh', busyKey: null });
    expect(secondsLeft(state.expiresAt, 1_000)).toBe(revealSeconds);
    expect(secondsLeft(state.expiresAt, 1_000 + 19_500)).toBe(1);

    state = revealReducer(state, { type: 'tick', now: 1_000 + revealSeconds * 1000 });
    expect(state.value).toBeNull();
    expect(state.key).toBeNull();

    state = revealReducer(state, { type: 'rate_limited', retryAfterSeconds: null, now: 0 });
    expect(secondsLeft(state.rateLimitedUntil, 0)).toBe(30);
    state = revealReducer(state, { type: 'rate_limited', retryAfterSeconds: 7, now: 0 });
    expect(secondsLeft(state.rateLimitedUntil, 0)).toBe(7);
    state = revealReducer(state, { type: 'tick', now: 7_000 });
    expect(state.rateLimitedUntil).toBeNull();
  });

  it('ignores a reveal result for a key that is no longer in flight', () => {
    const state = revealReducer(initialRevealState, { type: 'revealed', key: 'K', value: 'shh', now: 0 });
    expect(state.value).toBeNull();
  });
});

describe('keyboard shortcut and display helpers', () => {
  it('focuses search on / unless typing', () => {
    expect(envShortcut({ key: '/' })).toBe('focus-search');
    expect(envShortcut({ key: '/', targetTag: 'input' })).toBeNull();
    expect(envShortcut({ key: '/', targetEditable: true })).toBeNull();
    expect(envShortcut({ key: '/', metaKey: true })).toBeNull();
    expect(envShortcut({ key: 'a' })).toBeNull();
  });

  it('truncates long values and shortens hashes', () => {
    expect(truncateValue('short')).toBe('short');
    expect(truncateValue('x'.repeat(100))).toHaveLength(64);
    expect(truncateValue('x'.repeat(100)).endsWith('…')).toBe(true);
    expect(shortSha('sha256:abcdef1234567890')).toBe('abcdef12');
  });
});
