import { describe, expect, it } from 'vitest';

import { parseHash } from './hash.ts';

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
