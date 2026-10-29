import { describe, expect, it } from 'vitest';

import { containerPostureLabel } from './Dashboard.tsx';
import type { FleetContainersResponse } from '@/lib/api.ts';

function fleet(stale: boolean, critical: number): FleetContainersResponse {
  return {
    freshness: { reconciled_at: '2026-10-29T00:00:00Z', complete: true, stale, observed_count: 1 },
    containers: [{
      id: 'unknown-id', name: 'unknown-1', project: '', managed: false, state: 'running',
      classification: 'unknown', current: true, present: true, health: 'running',
      cpu_percent: 1, mem_bytes: 1, mem_limit: 2, restarts: 0,
      risk_counts: { critical, high: 0, medium: 0, low: 0 },
    }],
  };
}

describe('containerPostureLabel', () => {
  it('never summarizes a critical unknown container as healthy', () => {
    expect(containerPostureLabel(fleet(false, 1))).toBe('1 critical');
  });

  it('shows stale collection as degraded', () => {
    expect(containerPostureLabel(fleet(true, 0))).toBe('degraded collection');
  });
});
