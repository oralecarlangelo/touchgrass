import { describe, expect, it } from 'vitest';
import { updateJobMessage, updateVersionMessage, shouldReloadUpdatedConsole } from './UpdatesScreen';
import type { SelfUpdateStatus } from '@/lib/api';

const status: SelfUpdateStatus = {
  installed: '0.9.0', configured: true, can_install: false,
  release: { tag: 'v0.9.0', url: '', published_at: '' },
};

describe('update status explanations', () => {
  it('distinguishes a current release from a custom build', () => {
    expect(updateVersionMessage(status)).toContain('latest published release');
    expect(updateVersionMessage({ ...status, installed: '0.9.0-fleet.1' })).toContain('custom build');
  });
  it('explains setup for an unconfigured installation', () => {
    expect(updateVersionMessage({ ...status, installed: '0.8.0', configured: false })).toContain('one-time setup');
  });
});

describe('installer progress', () => {
  it('distinguishes queueing, backup and an older updater', () => {
    expect(updateJobMessage({ tag: 'v0.10.12', state: 'pending' })).toBe('Waiting for the installer to start.');
    expect(updateJobMessage({ tag: 'v0.10.12', state: 'running', phase: 'backing_up' })).toBe('Backing up the database.');
    expect(updateJobMessage({ tag: 'v0.10.12', state: 'running' })).toBe('The installer is running.');
    expect(updateJobMessage({ tag: 'v0.10.12', state: 'succeeded' })).toBeNull();
  });
});

describe('updated console bundle', () => {
  const done: SelfUpdateStatus = { ...status, installed: '0.10.0', job: { tag: 'v0.10.0', state: 'succeeded' } };
  it('reloads only after the exact requested version succeeds', () => {
    expect(shouldReloadUpdatedConsole('0.9.0', done, null)).toBe(true);
    expect(shouldReloadUpdatedConsole(null, done, 'v0.10.0')).toBe(true);
    expect(shouldReloadUpdatedConsole('0.10.0', done, null)).toBe(false);
    expect(shouldReloadUpdatedConsole(null, done, null)).toBe(false);
    expect(shouldReloadUpdatedConsole('0.9.0', { ...done, installed: '0.9.0' }, 'v0.10.0')).toBe(false);
    expect(shouldReloadUpdatedConsole('0.9.0', { ...done, job: { tag: 'v0.10.0', state: 'running' } }, null)).toBe(false);
  });
});
