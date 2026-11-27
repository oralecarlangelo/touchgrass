import { describe, expect, it, vi } from 'vitest';

import { hashUpdatedEvent, parseHash, writeHash } from './hash.ts';

describe('parseHash', () => {
  it('keeps the Help page deep-linkable', () => {
    expect(parseHash('#/help')).toEqual({ view: 'help', serviceId: null, tab: null });
  });

  it('keeps the Security page deep-linkable', () => {
    expect(parseHash('#/security')).toEqual({ view: 'security', serviceId: null, tab: null });
  });

  it('keeps the Environment page and a service tab deep-linkable', () => {
    expect(parseHash('#/environment')).toEqual({ view: 'environment', serviceId: null, tab: null });
    expect(parseHash('#/services/api/environment')).toEqual({
      view: 'services',
      serviceId: 'api',
      tab: 'environment',
    });
    expect(parseHash('#/services/api/..%2Fetc')).toEqual({ view: 'services', serviceId: 'api', tab: null });
  });
});

describe('writeHash', () => {
  it('preserves investigation parameters and notifies screens when the app changes routes', () => {
    const location = { hash: '#/issues?service_id=api' };
    const events = new EventTarget();
    const updated = vi.fn();
    events.addEventListener(hashUpdatedEvent, updated);
    vi.stubGlobal('window', Object.assign(events, {
      location,
      history: { replaceState: (_state: unknown, _unused: string, hash: string) => { location.hash = hash; } },
    }));

    try {
      writeHash('issues', null);
      expect(location.hash).toBe('#/issues?service_id=api');
      expect(updated).not.toHaveBeenCalled();
      writeHash('audit', null);
      expect(location.hash).toBe('#/audit');
      expect(updated).toHaveBeenCalledTimes(1);
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
