import { describe, expect, it } from 'vitest';
import {
  addBreadcrumb,
  captureException,
  captureMessage,
  close,
  flush,
  init,
  type InitOptions,
} from './index.js';

describe('default client', () => {
  it('no-ops before init', async () => {
    expect(() => captureException(new Error('x'))).not.toThrow();
    expect(() => captureMessage('x')).not.toThrow();
    expect(() => addBreadcrumb({ category: 'c' })).not.toThrow();
    await expect(flush()).resolves.toBe(true);
    await expect(close()).resolves.toBe(true);
  });

  it('survives garbage init', async () => {
    const client = init(null as unknown as InitOptions);

    expect(client.enabled()).toBe(false);
    await expect(close()).resolves.toBe(true);
  });

  it('replaces clients on re-init', async () => {
    init({ endpoint: '', key: '' });
    const second = init({ endpoint: '', key: '' });

    expect(second.enabled()).toBe(false);
    await expect(close()).resolves.toBe(true);
  });
});
