import { describe, expect, it } from 'vitest';

import { countUrgentFindings, filterSecurityFindings, severityRank } from './security.ts';
import type { SecurityFinding } from './api.ts';

function finding(id: number, severity: SecurityFinding['severity'], status: SecurityFinding['status'] = 'open'): SecurityFinding {
  return {
    id, fingerprint: `fp-${id}`, episode: 1, container_id: `c-${id}`, rule_id: `rule.${id}`,
    category: 'posture', severity, confidence: 'high', status, occurrence_count: 1,
    evidence: {}, opened_at: '2026-10-29T00:00:00Z', last_seen_at: '2026-10-29T00:00:00Z',
  };
}

describe('security presentation helpers', () => {
  it('orders severity deterministically', () => {
    expect(['low', 'critical', 'medium', 'high'].sort((a, b) => severityRank(b as SecurityFinding['severity']) - severityRank(a as SecurityFinding['severity']))).toEqual([
      'critical', 'high', 'medium', 'low',
    ]);
  });

  it('filters stably without mutating source order', () => {
    const source = [finding(1, 'high'), finding(2, 'critical'), finding(3, 'high', 'resolved')];
    expect(filterSecurityFindings(source, { severity: 'high', status: 'all', query: '' }).map((item) => item.id)).toEqual([1, 3]);
    expect(source.map((item) => item.id)).toEqual([1, 2, 3]);
  });

  it('counts active critical and high findings', () => {
    expect(countUrgentFindings([finding(1, 'critical'), finding(2, 'high'), finding(3, 'high', 'resolved')])).toEqual({ critical: 1, high: 1 });
  });
});
