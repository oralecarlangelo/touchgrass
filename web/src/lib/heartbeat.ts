import type { ContainerFreshness, ServiceFreshness } from './api.ts';

export type HeartbeatState = 'waiting' | 'current' | 'degraded' | 'stale' | 'unavailable';

export function snapshotHeartbeat(freshness: ServiceFreshness): HeartbeatState {
  if (freshness.observed_at === null) {
    return freshness.last_error_category === 'collection_failed' ? 'unavailable' : 'waiting';
  }
  if (freshness.stale) return 'stale';
  return freshness.complete ? 'current' : 'degraded';
}

export function inventoryHeartbeat(freshness: ContainerFreshness): {
  state: HeartbeatState;
  timeLabel: string;
} {
  const timeLabel = freshness.complete ? 'Last successful sample' : 'Last attempt';
  if (freshness.last_error_category === 'docker_inventory_unavailable') return { state: 'unavailable', timeLabel };
  if (freshness.reconciled_at === null) return { state: 'waiting', timeLabel };
  if (freshness.stale) return { state: 'stale', timeLabel };
  return { state: freshness.complete ? 'current' : 'degraded', timeLabel };
}
