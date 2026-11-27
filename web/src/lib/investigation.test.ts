import { describe, expect, it } from 'vitest';

import { alertInvestigation, investigationHref, readInvestigation } from './investigation.ts';

describe('alert investigation links', () => {
  it('passes the same service and fixed hour to every destination', () => {
    const filters = alertInvestigation({ service_id: 'api + jobs', created_at: '2026-11-28T09:00:00.427+08:00' });
    expect(filters).toEqual({
      serviceId: 'api + jobs',
      start: '2026-11-28T00:30:00.427Z',
      end: '2026-11-28T01:30:00.427Z',
    });
    if (filters === null) throw new Error('Expected an alert window');
    for (const view of ['logs', 'issues', 'audit'] as const) {
      const href = investigationHref(view, filters);
      expect(href).toMatch(new RegExp('^#/' + view + '\\?'));
      expect(readInvestigation(href)).toEqual(filters);
    }
  });

  it('handles service-less alerts and ignores unusable timestamps', () => {
    const filters = alertInvestigation({ service_id: '', created_at: '2026-11-28T01:00:00Z' });
    expect(filters?.serviceId).toBeUndefined();
    expect(alertInvestigation({ service_id: 'api', created_at: 'invalid' })).toBeNull();
    expect(readInvestigation('#/audit?start=invalid&service_id=api')).toEqual({ serviceId: 'api' });
  });
});
