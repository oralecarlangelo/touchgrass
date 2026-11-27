import { describe, expect, it } from 'vitest';

import { countUrgentFindings, findingCollector, recommendedResponse, severityRank } from './security.ts';
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

  it('labels collector episodes and gives a recovery action', () => {
    const item = { ...finding(1, 'high'), container_id: 'collector:security_process' };
    expect(findingCollector(item)).toBe('Runtime process scan');
    expect(recommendedResponse(item)).toContain('last successful sample');
  });

  it('counts active critical and high findings', () => {
    expect(countUrgentFindings([finding(1, 'critical'), finding(2, 'high'), finding(3, 'high', 'resolved')])).toEqual({ critical: 1, high: 1 });
  });

  it('gives traffic findings an edge-control response', () => {
    const item = finding(9, 'medium');
    item.category = 'traffic';
    item.rule_id = 'edge.malformed_server_action';
    expect(recommendedResponse(item)).toContain('edge control');
  });
});
