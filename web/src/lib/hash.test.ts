import { describe, expect, it } from 'vitest';

import { parseHash } from './hash.ts';

describe('parseHash', () => {
  it('keeps the Help page deep-linkable', () => {
    expect(parseHash('#/help')).toEqual({ view: 'help', serviceId: null });
  });

  it('keeps the Security page deep-linkable', () => {
    expect(parseHash('#/security')).toEqual({ view: 'security', serviceId: null });
  });
});
