// Pure logic for the Environment tab: validation, .env parsing, filtering,
// the apply-run state machine and the secret reveal state machine. Nothing
// here touches the DOM or the network, so every rule is unit-testable.

import type {
  EnvApplyStep,
  EnvDiff,
  EnvEntry,
  EnvEntryState,
  EnvRun,
  EnvValidation,
} from './api.ts';

// --- Validation ------------------------------------------------------------

export const envKeyPattern = /^[A-Z_][A-Z0-9_]*$/;
// Mirrors the server's oversize rule; the server stays authoritative.
export const maxEnvValueLength = 32 * 1024;
export const maxEnvKeyLength = 256;

export function validateEnvKey(key: string): string | null {
  if (key === '') {
    return 'Enter a key.';
  }
  if (key.length > maxEnvKeyLength) {
    return `Keys stop at ${maxEnvKeyLength} characters.`;
  }
  if (/[a-z]/.test(key) && envKeyPattern.test(key.toUpperCase())) {
    return 'Keys are upper-case: letters, digits and underscores, not starting with a digit.';
  }
  if (!envKeyPattern.test(key)) {
    return 'Use upper-case letters, digits and underscores, not starting with a digit.';
  }

  return null;
}

export function validateEnvValue(value: string): string | null {
  if (/[\r\n]/.test(value)) {
    return 'Values are a single line. Escape line breaks as \\n if the app expects them.';
  }
  if (value.length > maxEnvValueLength) {
    return `Values stop at ${maxEnvValueLength.toLocaleString()} characters.`;
  }

  return null;
}

// --- Secret guessing -------------------------------------------------------

const secretHints =
  /(SECRET|TOKEN|PASS(WORD|WD|PHRASE)?|PRIVATE|CREDENTIAL|API_?KEY|ACCESS_?KEY|SIGNING|SALT|DSN|AUTH|CLIENT_?SECRET|WEBHOOK_?KEY|ENCRYPTION|CERT|_KEY$)/;
const publicHints = /(PUBLIC|PUBLISHABLE|_URL$|_HOST$|_PORT$|_NAME$|_ID$)/;

export function looksSecret(key: string): boolean {
  const upper = key.toUpperCase();

  if (publicHints.test(upper)) {
    return false;
  }

  return secretHints.test(upper);
}

// --- .env paste parsing ----------------------------------------------------

export type ParsedEnvLineStatus = 'ok' | 'invalid_key' | 'invalid_value' | 'duplicate' | 'unparsable';

export interface ParsedEnvLine {
  line: number;
  raw: string;
  key: string;
  value: string;
  status: ParsedEnvLineStatus;
  message: string | null;
  secret: boolean;
}

function unescapeDoubleQuoted(value: string): string {
  return value.replace(/\\(.)/g, (_match, char: string) => {
    switch (char) {
      case 'n':
        return '\n';
      case 'r':
        return '\r';
      case 't':
        return '\t';
      default:
        return char;
    }
  });
}

function parseValue(rawValue: string): string {
  const trimmed = rawValue.trim();

  if (trimmed.length >= 2 && trimmed.startsWith('"')) {
    // Find the closing quote that is not escaped; anything after it is a comment.
    for (let index = 1; index < trimmed.length; index += 1) {
      if (trimmed[index] === '\\') {
        index += 1;
        continue;
      }
      if (trimmed[index] === '"') {
        return unescapeDoubleQuoted(trimmed.slice(1, index));
      }
    }

    return unescapeDoubleQuoted(trimmed.slice(1));
  }

  if (trimmed.length >= 2 && trimmed.startsWith("'")) {
    const close = trimmed.indexOf("'", 1);

    return close === -1 ? trimmed.slice(1) : trimmed.slice(1, close);
  }

  // Unquoted: strip an inline comment introduced by whitespace + '#'.
  const comment = trimmed.search(/\s#/);

  return (comment === -1 ? trimmed : trimmed.slice(0, comment)).trim();
}

// looksLikeDotenvPaste decides whether text dropped into the key field is a
// `.env` fragment (one or many KEY=VALUE lines) rather than a plain key.
export function looksLikeDotenvPaste(text: string): boolean {
  return /(^|\n)\s*(export\s+)?[A-Za-z_][A-Za-z0-9_]*\s*=/.test(text);
}

export function parseDotenvLines(text: string): ParsedEnvLine[] {
  const seen = new Map<string, number>();
  const result: ParsedEnvLine[] = [];

  text.split(/\r?\n/).forEach((raw, index) => {
    const line = index + 1;
    const stripped = raw.trim();

    if (stripped === '' || stripped.startsWith('#')) {
      return;
    }

    const match = /^(?:export\s+)?([^=\s]+)\s*=(.*)$/.exec(stripped);

    if (match === null) {
      result.push({
        line,
        raw,
        key: '',
        value: '',
        status: 'unparsable',
        message: 'Not a KEY=VALUE line.',
        secret: false,
      });
      return;
    }

    const key = match[1];
    const value = parseValue(match[2]);
    const keyError = validateEnvKey(key);
    const valueError = keyError === null ? validateEnvValue(value) : null;
    const duplicateOf = seen.get(key);
    let status: ParsedEnvLineStatus = 'ok';
    let message: string | null = null;

    if (keyError !== null) {
      status = 'invalid_key';
      message = keyError;
    } else if (valueError !== null) {
      status = 'invalid_value';
      message = valueError;
    } else if (duplicateOf !== undefined) {
      status = 'duplicate';
      message = `Same key as line ${duplicateOf}; skipped so the first value wins.`;
    } else {
      seen.set(key, line);
    }

    result.push({ line, raw, key, value, status, message, secret: looksSecret(key) });
  });

  return result;
}

// --- Table filtering & sorting --------------------------------------------

export interface EnvFilters {
  query: string;
  pendingOnly: boolean;
  secretsOnly: boolean;
  requiredOnly: boolean;
}

export type EnvSort = 'key' | 'updated';

export const defaultEnvFilters: EnvFilters = {
  query: '',
  pendingOnly: false,
  secretsOnly: false,
  requiredOnly: false,
};

export function filterEnvEntries(entries: EnvEntry[], filters: EnvFilters): EnvEntry[] {
  const query = filters.query.trim().toUpperCase();

  return entries.filter(
    (entry) =>
      (query === '' || entry.key.toUpperCase().includes(query)) &&
      (!filters.pendingOnly || entry.state !== 'applied') &&
      (!filters.secretsOnly || entry.is_secret) &&
      (!filters.requiredOnly || entry.required),
  );
}

export function sortEnvEntries(entries: EnvEntry[], sort: EnvSort): EnvEntry[] {
  const copy = [...entries];

  if (sort === 'updated') {
    copy.sort((a, b) => {
      const byTime = Date.parse(b.updated_at) - Date.parse(a.updated_at);

      return Number.isNaN(byTime) || byTime === 0 ? a.key.localeCompare(b.key) : byTime;
    });
    return copy;
  }

  copy.sort((a, b) => a.key.localeCompare(b.key));
  return copy;
}

// --- Pending summary -------------------------------------------------------

export interface PendingCounts {
  added: number;
  changed: number;
  removed: number;
  total: number;
}

export function pendingCounts(entries: EnvEntry[]): PendingCounts {
  const counts = { added: 0, changed: 0, removed: 0, total: 0 };

  for (const entry of entries) {
    if (entry.state === 'applied') {
      continue;
    }
    counts[entry.state] += 1;
    counts.total += 1;
  }

  return counts;
}

export function countsFromDiff(diff: EnvDiff): PendingCounts {
  return {
    added: diff.added.length,
    changed: diff.changed.length,
    removed: diff.removed.length,
    total: diff.added.length + diff.changed.length + diff.removed.length,
  };
}

export function describePending(counts: PendingCounts): string {
  const head = counts.total === 1 ? '1 change not applied' : `${counts.total} changes not applied`;
  const parts = [
    counts.added > 0 ? `${counts.added} added` : null,
    counts.changed > 0 ? `${counts.changed} changed` : null,
    counts.removed > 0 ? `${counts.removed} removed` : null,
  ].filter((part): part is string => part !== null);

  return [head, ...parts].join(' · ');
}

function joinKeys(keys: string[]): string {
  const shown = keys.slice(0, 3);
  const rest = keys.length - shown.length;

  if (rest > 0) {
    return `${shown.join(', ')} and ${rest} more`;
  }
  if (shown.length <= 1) {
    return shown.join('');
  }

  return `${shown.slice(0, -1).join(', ')} and ${shown[shown.length - 1]}`;
}

// suggestSummary turns a diff into a revision summary an operator would
// write by hand: "Rotate GCASH_KEY", "Add SMTP_HOST and SMTP_PORT".
export function suggestSummary(diff: EnvDiff, entries: EnvEntry[] = []): string {
  const secretKeys = new Set(entries.filter((entry) => entry.is_secret).map((entry) => entry.key));
  const rotated = diff.changed.filter((key) => secretKeys.has(key) || looksSecret(key));
  const changed = diff.changed.filter((key) => !rotated.includes(key));
  const parts: string[] = [];

  if (rotated.length > 0) {
    parts.push(`Rotate ${joinKeys(rotated)}`);
  }
  if (changed.length > 0) {
    parts.push(`Update ${joinKeys(changed)}`);
  }
  if (diff.added.length > 0) {
    parts.push(`Add ${joinKeys(diff.added)}`);
  }
  if (diff.removed.length > 0) {
    parts.push(`Remove ${joinKeys(diff.removed)}`);
  }

  return parts.join('; ');
}

// --- Undo of a staged change ----------------------------------------------

export type UndoPlan =
  | { kind: 'delete' }
  | { kind: 'restore'; value: string; is_secret: boolean }
  | { kind: 'unavailable'; reason: string };

export function undoPlan(entry: EnvEntry): UndoPlan | null {
  switch (entry.state) {
    case 'applied':
      return null;
    case 'added':
      return { kind: 'delete' };
    case 'changed':
      if (typeof entry.applied_value === 'string') {
        return { kind: 'restore', value: entry.applied_value, is_secret: entry.is_secret };
      }

      return {
        kind: 'unavailable',
        reason: entry.is_secret
          ? 'The applied secret is never sent to the browser, so it cannot be put back here. Discard all changes to keep it.'
          : 'The applied value was not returned, so it cannot be put back here. Discard all changes to keep it.',
      };
    case 'removed':
      if (typeof entry.value === 'string') {
        return { kind: 'restore', value: entry.value, is_secret: entry.is_secret };
      }

      return {
        kind: 'unavailable',
        reason:
          'The applied secret is never sent to the browser, so it cannot be re-added here. Discard all changes to keep it.',
      };
    default:
      return null;
  }
}

// --- Validation hints ------------------------------------------------------

const missingPattern =
  /missing(?:\s+required)?(?:\s+(?:key|keys|variable|variables|env|var|vars))?[:\s]+([A-Z_][A-Z0-9_]*(?:\s*,\s*[A-Z_][A-Z0-9_]*)*)/i;

// missingRequiredKeys returns the keys the validator reported as missing,
// preferring a structured list and falling back to the validator's lines.
export function missingRequiredKeys(validation: EnvValidation | null, entries: EnvEntry[]): string[] {
  if (validation === null) {
    return [];
  }

  const present = new Set(entries.filter((entry) => entry.state !== 'removed').map((entry) => entry.key));
  let keys: string[] = [];

  if (Array.isArray(validation.missing_required)) {
    keys = validation.missing_required;
  } else if (Array.isArray(validation.required_keys)) {
    keys = validation.required_keys;
  } else if (!validation.ok) {
    for (const line of validation.lines) {
      const match = missingPattern.exec(line);

      if (match !== null) {
        keys.push(...match[1].split(',').map((key) => key.trim()));
      }
    }
  }

  return [...new Set(keys)].filter((key) => envKeyPattern.test(key) && !present.has(key));
}

// --- Apply run state machine ----------------------------------------------

export const applyStepOrder = ['render', 'validate', 'write', 'restart', 'probe'] as const;
export type ApplyPipelineStep = (typeof applyStepOrder)[number];

export const applyStepCopy: Record<ApplyPipelineStep, { label: string; detail: string }> = {
  render: { label: 'Render', detail: 'Build the new file from the staged entries.' },
  validate: { label: 'Validate', detail: 'Run the validator against the rendered file.' },
  write: { label: 'Write', detail: 'Replace the file on the host, keeping the previous one.' },
  restart: { label: 'Restart', detail: 'Restart the service so it reads the new file.' },
  probe: { label: 'Probe', detail: 'Check the health URL; a failure restores the previous file.' },
};

export type StepStatus = 'pending' | 'running' | 'done' | 'failed';

export interface RunLogLine {
  at: string;
  step: string;
  message: string;
}

export interface ApplyRunState {
  runId: string;
  status: 'running' | 'done' | 'failed';
  steps: Record<ApplyPipelineStep, StepStatus>;
  failedStep: ApplyPipelineStep | null;
  lines: RunLogLine[];
  validatorLines: string[];
  error: string | null;
  restored: boolean;
  startedAt: string;
  finishedAt: string | null;
}

export interface EnvApplyEvent {
  run_id: string;
  service_id: string;
  step: EnvApplyStep;
  message: string;
  at: string;
}

const applySteps = new Set<string>(['render', 'validate', 'write', 'restart', 'probe', 'done', 'failed']);

export function parseEnvApplyEvent(data: string): EnvApplyEvent | null {
  try {
    const parsed = JSON.parse(data) as Partial<EnvApplyEvent> & { type?: string };

    if (parsed.type !== undefined && parsed.type !== 'env.apply') {
      return null;
    }
    if (
      typeof parsed.run_id !== 'string' ||
      typeof parsed.service_id !== 'string' ||
      typeof parsed.step !== 'string' ||
      !applySteps.has(parsed.step)
    ) {
      return null;
    }

    return {
      run_id: parsed.run_id,
      service_id: parsed.service_id,
      step: parsed.step,
      message: typeof parsed.message === 'string' ? parsed.message : '',
      at: typeof parsed.at === 'string' ? parsed.at : new Date().toISOString(),
    };
  } catch {
    return null;
  }
}

export function initialRunState(runId: string, startedAt = new Date().toISOString()): ApplyRunState {
  return {
    runId,
    status: 'running',
    steps: { render: 'pending', validate: 'pending', write: 'pending', restart: 'pending', probe: 'pending' },
    failedStep: null,
    lines: [],
    validatorLines: [],
    error: null,
    restored: false,
    startedAt,
    finishedAt: null,
  };
}

// stepFromMessage reads the pipeline step a run message names ("validate: …").
function stepFromMessage(message: string): ApplyPipelineStep | null {
  const match = /^(render|validate|write|restart|probe):/.exec(message.trim());

  return match !== null && isPipelineStep(match[1]) ? match[1] : null;
}

// markEarlierDone completes every step before `step` that has not failed.
function markEarlierDone(steps: Record<ApplyPipelineStep, StepStatus>, step: ApplyPipelineStep): void {
  const index = applyStepOrder.indexOf(step);

  applyStepOrder.forEach((candidate, position) => {
    if (position < index && steps[candidate] !== 'failed') {
      steps[candidate] = 'done';
    }
  });
}

function isPipelineStep(step: string): step is ApplyPipelineStep {
  return (applyStepOrder as readonly string[]).includes(step);
}

function splitLines(message: string): string[] {
  return message
    .split(/\r?\n/)
    .map((line) => line.trimEnd())
    .filter((line) => line !== '');
}

const maxRunLines = 300;

function appendLine(lines: RunLogLine[], line: RunLogLine): RunLogLine[] {
  return [...lines.slice(-(maxRunLines - 1)), line];
}

// reduceRunEvent advances the stepper from one SSE event. A step event marks
// that step running and every earlier step done; `done` completes the run;
// `failed` pins the failure on the step that was running.
export function reduceRunEvent(state: ApplyRunState, event: EnvApplyEvent): ApplyRunState {
  if (event.run_id !== state.runId || state.status !== 'running') {
    return state;
  }

  const lines = appendLine(state.lines, { at: event.at, step: event.step, message: event.message });

  if (event.step === 'done') {
    const steps = { ...state.steps };

    for (const step of applyStepOrder) {
      steps[step] = 'done';
    }

    return { ...state, steps, lines, status: 'done', finishedAt: event.at };
  }

  if (event.step === 'failed') {
    // A run can finish before the stream is attached; the failure message is
    // prefixed with the step name ("validate: …"), so fall back to it.
    const running =
      applyStepOrder.find((step) => state.steps[step] === 'running') ?? stepFromMessage(event.message);
    const steps = { ...state.steps };

    if (running !== null) {
      markEarlierDone(steps, running);
      steps[running] = 'failed';
    }

    const messageLines = splitLines(event.message);
    const validatorLines =
      running === 'validate' && messageLines.length > 0
        ? [...state.validatorLines, ...messageLines]
        : state.validatorLines;
    // Whether the previous file came back is the server's call (a refused
    // write_cmd installs nothing); the run record sets it on reconcile.
    const restored = false;

    return {
      ...state,
      steps,
      lines,
      status: 'failed',
      failedStep: running,
      validatorLines,
      error: messageLines[0] ?? 'The apply failed.',
      restored,
      finishedAt: event.at,
    };
  }

  if (!isPipelineStep(event.step)) {
    return { ...state, lines };
  }

  const steps = { ...state.steps };
  const index = applyStepOrder.indexOf(event.step);

  applyStepOrder.forEach((step, position) => {
    if (position < index && steps[step] !== 'failed') {
      steps[step] = 'done';
    }
  });
  steps[event.step] = 'running';

  // Validator output streams on the validate step before any failure.
  const validatorLines =
    event.step === 'validate' && event.message !== '' && /\n/.test(event.message)
      ? [...state.validatorLines, ...splitLines(event.message)]
      : state.validatorLines;

  return { ...state, steps, lines, validatorLines };
}

// mergeRun folds a polled run record into the state. It never moves a
// finished run back to running and never discards SSE log lines.
export function mergeRun(state: ApplyRunState, run: EnvRun): ApplyRunState {
  if (run.id !== state.runId) {
    return state;
  }

  const steps = { ...state.steps };
  let failedStep = state.failedStep;

  for (const step of run.steps) {
    if (!isPipelineStep(step.name)) {
      continue;
    }
    // The server says `succeeded`; the stepper says `done`.
    const stepStatus: StepStatus = step.status === 'succeeded' ? 'done' : step.status;
    markEarlierDone(steps, step.name);
    steps[step.name] = stepStatus;
    if (stepStatus === 'failed') {
      failedStep = step.name;
    }
  }

  const status = state.status !== 'running' ? state.status : run.status;

  if (status === 'failed' && failedStep === null) {
    // Older records leave the failing step "running"; pin the failure on it,
    // or on the step named in the error.
    const culprit =
      applyStepOrder.find((step) => steps[step] === 'running') ??
      stepFromMessage(typeof run.error === 'string' ? run.error : '');

    if (culprit !== null) {
      markEarlierDone(steps, culprit);
      steps[culprit] = 'failed';
      failedStep = culprit;
    }
  }
  const polledLines = run.steps
    .filter((step) => step.message !== '')
    .map((step) => ({ at: step.at, step: step.name, message: step.message }));
  const known = new Set(state.lines.map((line) => `${line.step}|${line.message}`));
  const lines = polledLines.reduce(
    (acc, line) => (known.has(`${line.step}|${line.message}`) ? acc : appendLine(acc, line)),
    state.lines,
  );
  const validatorLines =
    state.validatorLines.length > 0 ? state.validatorLines : (run.validation_lines ?? []);

  if (status === 'done') {
    for (const step of applyStepOrder) {
      steps[step] = 'done';
    }
  }

  return {
    ...state,
    status,
    steps,
    failedStep: status === 'failed' ? failedStep : null,
    lines,
    validatorLines,
    error: state.error ?? (typeof run.error === 'string' && run.error !== '' ? run.error : null),
    restored: state.restored || run.restored === true,
    startedAt: run.started_at !== '' ? run.started_at : state.startedAt,
    finishedAt: state.finishedAt ?? run.finished_at,
  };
}

export function elapsedSeconds(startedAt: string, until: string | null, now: number): number {
  const start = Date.parse(startedAt);
  const end = until === null ? now : Date.parse(until);

  if (Number.isNaN(start) || Number.isNaN(end)) {
    return 0;
  }

  return Math.max(0, Math.floor((end - start) / 1000));
}

// --- Secret reveal state machine ------------------------------------------

export const revealSeconds = 20;
export const defaultRevealRetrySeconds = 30;

export interface RevealState {
  key: string | null;
  value: string | null;
  expiresAt: number | null;
  busyKey: string | null;
  rateLimitedUntil: number | null;
  error: string | null;
}

export const initialRevealState: RevealState = {
  key: null,
  value: null,
  expiresAt: null,
  busyKey: null,
  rateLimitedUntil: null,
  error: null,
};

export type RevealAction =
  | { type: 'start'; key: string }
  | { type: 'revealed'; key: string; value: string; now: number }
  | { type: 'rate_limited'; retryAfterSeconds: number | null; now: number }
  | { type: 'failed'; message: string }
  | { type: 'tick'; now: number }
  | { type: 'hide' };

export function revealReducer(state: RevealState, action: RevealAction): RevealState {
  switch (action.type) {
    case 'start':
      return { ...state, busyKey: action.key, error: null };
    case 'revealed':
      if (state.busyKey !== action.key) {
        return state;
      }

      return {
        key: action.key,
        value: action.value,
        expiresAt: action.now + revealSeconds * 1000,
        busyKey: null,
        rateLimitedUntil: null,
        error: null,
      };
    case 'rate_limited':
      return {
        ...state,
        busyKey: null,
        rateLimitedUntil: action.now + (action.retryAfterSeconds ?? defaultRevealRetrySeconds) * 1000,
        error: null,
      };
    case 'failed':
      return { ...state, busyKey: null, error: action.message };
    case 'tick': {
      let next = state;

      if (state.expiresAt !== null && action.now >= state.expiresAt) {
        next = { ...next, key: null, value: null, expiresAt: null };
      }
      if (state.rateLimitedUntil !== null && action.now >= state.rateLimitedUntil) {
        next = { ...next, rateLimitedUntil: null };
      }

      return next;
    }
    case 'hide':
      return { ...state, key: null, value: null, expiresAt: null };
    default:
      return state;
  }
}

export function secondsLeft(until: number | null, now: number): number {
  if (until === null) {
    return 0;
  }

  return Math.max(0, Math.ceil((until - now) / 1000));
}

// --- Misc display helpers --------------------------------------------------

export const envDocsHref = '/docs/env.html';

// envKeyEnvVar names the host variable that points at the encryption key
// file (the server also accepts the raw key in TOUCHGRASS_ENV_KEY).
export const envKeyEnvVar = 'TOUCHGRASS_ENV_KEY_FILE';

// envTargetConfigSnippet is the JSON an admin adds to the service config to
// name the file on the host: the `env` block of the service config
// (`EnvConfig` on the server), with the target nested under `target`.
export function envTargetConfigSnippet(serviceId: string): string {
  return JSON.stringify(
    {
      env: {
        target: {
          path: `/srv/${serviceId}/.env`,
          owner: 'app:app',
          mode: '0600',
        },
        validate_cmd: `/srv/${serviceId}/bin/validate-env.sh`,
        restart_cmd: `/srv/${serviceId}/bin/restart.sh`,
      },
    },
    null,
    2,
  );
}

export const longValueThreshold = 64;

export function truncateValue(value: string, max = longValueThreshold): string {
  if (value.length <= max) {
    return value;
  }

  return `${value.slice(0, max - 1)}…`;
}

export function shortSha(sha: string): string {
  return sha.replace(/^sha256:/, '').slice(0, 8);
}

export function stateLabel(state: EnvEntryState): string {
  switch (state) {
    case 'added':
      return 'Added';
    case 'changed':
      return 'Changed';
    case 'removed':
      return 'Removed';
    default:
      return '';
  }
}

export const maskedValue = '••••••••';

// envShortcut maps a keydown to a tab-level action. `/` focuses search
// unless the operator is already typing somewhere.
export function envShortcut(event: {
  key: string;
  metaKey?: boolean;
  ctrlKey?: boolean;
  altKey?: boolean;
  targetTag?: string;
  targetEditable?: boolean;
}): 'focus-search' | null {
  if (event.metaKey === true || event.ctrlKey === true || event.altKey === true) {
    return null;
  }

  const tag = (event.targetTag ?? '').toUpperCase();
  const typing = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || event.targetEditable === true;

  if (event.key === '/' && !typing) {
    return 'focus-search';
  }

  return null;
}
