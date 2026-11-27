import { describe, expect, it } from 'vitest';

import { inventoryHeartbeat, snapshotHeartbeat } from './heartbeat.ts';

const observedAt = '2026-11-27T05:00:00Z';

describe('monitoring heartbeat labels', () => {
  it('distinguishes first collection from a failed collection', () => {
    const freshness = { observed_at: null, complete: false, stale: true };
    expect(snapshotHeartbeat(freshness)).toBe('waiting');
    expect(snapshotHeartbeat({ ...freshness, last_error_category: 'collection_failed' })).toBe('unavailable');
    expect(snapshotHeartbeat({ ...freshness, observed_at: observedAt, last_error_category: 'collection_failed' })).toBe('stale');
  });

  it('labels incomplete reconciliation as an attempt even when it has a recent timestamp', () => {
    const inventory = { reconciled_at: observedAt, complete: false, stale: false, observed_count: 3 };
    expect(inventoryHeartbeat(inventory)).toEqual({ state: 'degraded', timeLabel: 'Last attempt' });
    expect(inventoryHeartbeat({ ...inventory, last_error_category: 'docker_inventory_unavailable' }))
      .toEqual({ state: 'unavailable', timeLabel: 'Last attempt' });
    expect(inventoryHeartbeat({ ...inventory, complete: true, stale: true }))
      .toEqual({ state: 'stale', timeLabel: 'Last successful sample' });
  });
});
