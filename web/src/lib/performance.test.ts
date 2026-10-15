import { describe, expect, it } from 'vitest';

import {
  collectorState,
  formatPercentile,
  mergeTimelineSeries,
  performanceDrilldownHref,
  servicePerformanceState,
} from './performance.ts';
import { performanceApiPath } from './api.ts';

describe('mergeTimelineSeries', () => {
  it('sorts mismatched buckets and preserves unavailable sources', () => {
    const result = mergeTimelineSeries(
      [
        {
          bucket_start: '2026-10-16T02:02:00Z',
          request_count: 4,
          error_count: 1,
          error_rate: 25,
          p50_ms: 25,
          p95_ms: 100,
          p99_ms: 250,
          log_errors: 1,
          log_warnings: 0,
          cpu_percent: null,
          memory_percent: null,
          postgres_connection_pct: 72,
          redis_evictions: null,
        },
        {
          bucket_start: '2026-10-16T02:00:00Z',
          request_count: 2,
          error_count: 0,
          error_rate: 0,
          p50_ms: 10,
          p95_ms: 25,
          p99_ms: 50,
          log_errors: 0,
          log_warnings: 2,
          cpu_percent: 18,
          memory_percent: 43,
          postgres_connection_pct: null,
          redis_evictions: 0,
        },
      ],
      null,
      null,
    );

    expect(result.points.map((point) => point.bucket_start)).toEqual([
      '2026-10-16T02:00:00.000Z',
      '2026-10-16T02:02:00.000Z',
    ]);
    expect(result.points[1]?.cpu_percent).toBeNull();
    expect(result.markers).toEqual([]);
  });

  it('drops invalid timestamps and orders deploy and runtime markers deterministically', () => {
    const result = mergeTimelineSeries(
      [
        {
          bucket_start: 'not-a-time',
          request_count: 1,
          error_count: 0,
          error_rate: 0,
          p50_ms: 0,
          p95_ms: 0,
          p99_ms: 0,
          log_errors: 0,
          log_warnings: 0,
          cpu_percent: null,
          memory_percent: null,
          postgres_connection_pct: null,
          redis_evictions: null,
        },
      ],
      [
        {
          source_event_id: 'runtime-2',
          occurred_at: '2026-10-16T02:05:00Z',
          service_id: 'api',
          environment: 'prod',
          event_type: 'restart',
          container_name: 'api-1',
          compose_service: 'api',
          expected: false,
        },
      ],
      [
        {
          id: 8,
          service_id: 'api',
          sha: 'abc123',
          actor: 'deploy',
          type: 'deploy',
          outcome: 'success',
          started_at: '2026-10-16T02:05:00Z',
          finished_at: '2026-10-16T02:06:00Z',
          duration_secs: 60,
          downtime_secs: 0,
          notes: '',
          created_at: '2026-10-16T02:04:00Z',
        },
        {
          id: 7,
          service_id: 'api',
          sha: 'def456',
          actor: 'deploy',
          type: 'deploy',
          outcome: 'failure',
          started_at: null,
          finished_at: null,
          duration_secs: null,
          downtime_secs: null,
          notes: '',
          created_at: 'invalid',
        },
      ],
    );

    expect(result.points).toEqual([]);
    expect(result.markers.map((marker) => marker.kind)).toEqual(['deployment', 'runtime']);
    expect(result.markers.map((marker) => marker.at)).toEqual([
      '2026-10-16T02:05:00.000Z',
      '2026-10-16T02:05:00.000Z',
    ]);
  });
});

describe('formatPercentile', () => {
  it('formats finite millisecond values without inventing unavailable data', () => {
    expect(formatPercentile(0)).toBe('0 ms');
    expect(formatPercentile(0.4)).toBe('<1 ms');
    expect(formatPercentile(12.4)).toBe('12 ms');
    expect(formatPercentile(1250)).toBe('1.25 s');
    expect(formatPercentile(null)).toBe('—');
    expect(formatPercentile(Number.NaN)).toBe('—');
  });
});

describe('collectorState', () => {
  it('distinguishes setup, stale, degraded, and healthy collectors', () => {
    expect(
      collectorState({ source: 'nginx_http', dropped_records: 0, stale: true }),
    ).toBe('not-configured');
    expect(
      collectorState({
        source: 'nginx_http',
        last_success_at: '0001-01-01T00:00:00Z',
        dropped_records: 0,
        stale: true,
      }),
    ).toBe('not-configured');
    expect(
      collectorState({
        source: 'postgres',
        last_success_at: '2026-10-16T01:00:00Z',
        dropped_records: 0,
        stale: true,
      }),
    ).toBe('stale');
    expect(
      collectorState({
        source: 'redis',
        last_success_at: '2026-10-16T02:00:00Z',
        last_error: 'timeout',
        dropped_records: 0,
        stale: false,
      }),
    ).toBe('degraded');
    expect(
      collectorState({
        source: 'docker_events',
        last_success_at: '2026-10-16T02:00:00Z',
        dropped_records: 0,
        stale: false,
      }),
    ).toBe('healthy');
  });
});

describe('service performance view model', () => {
  it('scopes every performance request to the selected service', () => {
    for (const resource of ['summary', 'timeline', 'routes', 'postgres', 'redis'] as const) {
      expect(performanceApiPath(resource, { serviceId: 'ticketnation api', limit: 50 })).toBe(
        `/api/services/ticketnation%20api/performance/${resource}?limit=50`,
      );
    }
  });

  it('keeps safe unknown-service and container-only states', () => {
    expect(
      servicePerformanceState({
        serviceId: '',
        loading: false,
        metricCount: 0,
        signalCount: 0,
        failureCount: 0,
      }),
    ).toBe('unknown-service');
    expect(
      servicePerformanceState({
        serviceId: 'api',
        loading: false,
        metricCount: 12,
        signalCount: 0,
        failureCount: 5,
      }),
    ).toBe('container-only');
  });

  it('retains service and time range in drill-down links', () => {
    expect(
      performanceDrilldownHref('logs', {
        serviceId: 'api blue',
        start: '2026-10-16T01:00:00Z',
        end: '2026-10-16T02:00:00Z',
      }),
    ).toBe(
      '#/logs?service_id=api+blue&start=2026-10-16T01%3A00%3A00.000Z&end=2026-10-16T02%3A00%3A00.000Z',
    );
  });
});
