import type {
  CollectorStatusView,
  Deploy,
  PerformanceTimelinePoint,
  RuntimeMarker,
} from './api.ts';

export type CollectorState = 'healthy' | 'degraded' | 'stale' | 'not-configured';

export interface TimelineMarker {
  id: string;
  at: string;
  kind: 'deployment' | 'runtime';
  label: string;
  serviceId: string;
  expected: boolean;
}

export interface CorrelationTimelineModel {
  points: PerformanceTimelinePoint[];
  markers: TimelineMarker[];
}

function normalizedTime(value: string | null | undefined): string | null {
  if (value === null || value === undefined || value === '') {
    return null;
  }

  const parsed = new Date(value);

  return Number.isNaN(parsed.getTime()) || parsed.getUTCFullYear() <= 1 ? null : parsed.toISOString();
}

export function mergeTimelineSeries(
  points: PerformanceTimelinePoint[] | null | undefined,
  events: RuntimeMarker[] | null | undefined,
  deployments: Deploy[] | null | undefined,
): CorrelationTimelineModel {
  const validPoints = (points ?? [])
    .flatMap((point) => {
      const at = normalizedTime(point.bucket_start);

      return at === null ? [] : [{ ...point, bucket_start: at }];
    })
    .sort((left, right) => left.bucket_start.localeCompare(right.bucket_start));

  const runtimeMarkers: TimelineMarker[] = (events ?? []).flatMap((event) => {
    const at = normalizedTime(event.occurred_at);

    return at === null
      ? []
      : [
          {
            id: `runtime:${event.source_event_id}`,
            at,
            kind: 'runtime',
            label: `${event.event_type} · ${event.container_name || event.compose_service}`,
            serviceId: event.service_id,
            expected: event.expected,
          },
        ];
  });

  const deployMarkers: TimelineMarker[] = (deployments ?? []).flatMap((deploy) => {
    const at = normalizedTime(deploy.started_at ?? deploy.created_at);

    return at === null
      ? []
      : [
          {
            id: `deploy:${deploy.id}`,
            at,
            kind: 'deployment',
            label: `${deploy.type} · ${deploy.outcome}`,
            serviceId: deploy.service_id,
            expected: deploy.outcome === 'success',
          },
        ];
  });

  const markers = [...deployMarkers, ...runtimeMarkers].sort((left, right) => {
    const timeOrder = left.at.localeCompare(right.at);

    if (timeOrder !== 0) {
      return timeOrder;
    }

    return left.kind.localeCompare(right.kind);
  });

  return { points: validPoints, markers };
}

export function formatPercentile(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value < 0) {
    return '—';
  }

  if (value === 0) {
    return '0 ms';
  }

  if (value < 1) {
    return '<1 ms';
  }

  if (value >= 1000) {
    return `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2).replace(/\.0+$/, '')} s`;
  }

  return `${Math.round(value)} ms`;
}

export function collectorState(status: CollectorStatusView): CollectorState {
  if (normalizedTime(status.last_success_at) === null) {
    return 'not-configured';
  }

  if (status.stale) {
    return 'stale';
  }

  if ((status.last_error ?? '') !== '' || status.dropped_records > 0) {
    return 'degraded';
  }

  return 'healthy';
}
