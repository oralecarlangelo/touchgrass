import { describe, expect, it } from 'vitest';

import { filterFleetContainers } from './FleetScreen.tsx';
import type { FleetContainer } from '@/lib/api.ts';

function fixture(id: string, state: string, classification: FleetContainer['classification'], critical = 0): FleetContainer {
  return {
    id, name: 'reused-name', project: 'app', managed: classification === 'managed', state,
    classification, current: true, present: true, health: state,
    cpu_percent: state === 'running' ? 2 : null, mem_bytes: state === 'running' ? 3 : null,
    mem_limit: state === 'running' ? 4 : null, restarts: state === 'running' ? 0 : null,
    risk_counts: { critical, high: 0, medium: 0, low: 0 },
  };
}

describe('filterFleetContainers', () => {
  const containers = [
    fixture('old-id', 'exited', 'unknown'),
    fixture('new-id', 'running', 'managed'),
    fixture('risk-id', 'running', 'unknown', 1),
  ];

  it('covers all, running, stopped, unknown, and risky filters', () => {
    expect(filterFleetContainers(containers, 'all', '')).toHaveLength(3);
    expect(filterFleetContainers(containers, 'running', '')).toHaveLength(2);
    expect(filterFleetContainers(containers, 'stopped', '').map((item) => item.id)).toEqual(['old-id']);
    expect(filterFleetContainers(containers, 'unknown', '')).toHaveLength(2);
    expect(filterFleetContainers(containers, 'risky', '').map((item) => item.id)).toEqual(['risk-id']);
  });

  it('preserves unique instance IDs when a name is reused', () => {
    const keys = filterFleetContainers(containers, 'all', '').map((item) => item.id);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).toContain('old-id');
    expect(keys).toContain('new-id');
  });
});
