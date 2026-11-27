import type {
  CollectorStatusView,
  Deploy,
  DatabaseView,
  PerformanceTimelinePoint,
  RuntimeMarker,
  ServiceView,
} from './api.ts';

export type CollectorState = 'healthy' | 'degraded' | 'stale' | 'not-configured' | 'disabled';
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
  if (status.last_error === 'disabled') {
    return 'disabled';
  }
  if (status.last_error) {
    return 'degraded';
  }
  if (collectorLastSuccessAt(status) === null) {
    return 'not-configured';
  }

  if (status.stale) {
    return 'stale';
  }

  if (!status.coverage_complete) {
    return 'degraded';
  }

  return 'healthy';
}

export function collectorGuidance(status: CollectorStatusView): string | null {
  switch (status.last_error) {
    case 'postgres_statements_unavailable':
      return 'Core metrics are available. Enable pg_stat_statements for query-level coverage.';
    case 'postgres_statements_extension_missing':
      return 'Core metrics are available. Loading query-level metrics requires PostgreSQL preload, restart, and CREATE EXTENSION pg_stat_statements.';
    case 'postgres_statements_preload_required':
      return 'Core metrics are available. Add pg_stat_statements to shared_preload_libraries and restart PostgreSQL.';
    case 'postgres_statements_invalid':
      return 'Core metrics are available, but pg_stat_statements returned an unsupported result.';
    case 'evaluation_failed':
      if (status.source === 'security_diff') {
        return 'Enable TOUCHGRASS_SECURITY_DIFF_INTERVAL and verify Docker access for writable-layer scans.';
      }
      return 'The last evaluation failed. Verify Docker access and retry the collector.';
    case 'docker_diff_unavailable':
      return 'Writable-layer data is incomplete. Verify Docker access for every running container.';
    default:
      break;
  }

  if (collectorLastSuccessAt(status) !== null && status.last_error !== 'disabled') {
    return null;
  }

  switch (status.source) {
    case 'nginx':
      return 'Set TOUCHGRASS_NGINX_TIMING_LOG and TOUCHGRASS_PERFORMANCE_SERVICE_ID to collect HTTP timings.';
    case 'postgres':
      return 'Set TOUCHGRASS_POSTGRES_CONTAINER, user, database, and TOUCHGRASS_PERFORMANCE_SERVICE_ID.';
    case 'redis':
      return 'Set TOUCHGRASS_REDIS_CONTAINER and TOUCHGRASS_PERFORMANCE_SERVICE_ID.';
    case 'security_diff':
      return 'Set TOUCHGRASS_SECURITY_DIFF_INTERVAL to enable writable-layer scans.';
    default:
      return null;
  }
}

export function collectorSetupSnippet(
  collectors: CollectorStatusView[],
  services: ServiceView[],
  serviceId: string,
  databaseSources: DatabaseView[] = [],
): string {
  const missing = new Set(
    collectors
      .filter((collector) => !collectorLastSuccessAt(collector) || collectorState(collector) === 'disabled')
      .map((collector) => collector.source),
  );
  const service = services.find((candidate) => candidate.id === serviceId) ?? services[0];
  if (service === undefined) {
    return '';
  }
  const lines = [`TOUCHGRASS_PERFORMANCE_SERVICE_ID=${JSON.stringify(service.id)}`];
  const findContainer = (pattern: RegExp) => {
    const managed = service.containers.find(
      (container) => pattern.test(container.name) || pattern.test(container.image),
    )?.name;
    if (managed) return managed;
    const discovered = [...new Set(databaseSources.flatMap((database) =>
      database.container && database.status !== 'stopped' && pattern.test(database.label) ? [database.container] : [],
    ))];
    return discovered.length === 1 ? discovered[0] : undefined;
  };
  const postgres = missing.has('postgres') ? findContainer(/postgres/i) : undefined;
  const redis = missing.has('redis') ? findContainer(/redis|valkey/i) : undefined;
  if (postgres !== undefined) {
    lines.push(`TOUCHGRASS_POSTGRES_CONTAINER=${JSON.stringify(postgres)}`);
  }
  if (redis !== undefined) {
    lines.push(`TOUCHGRASS_REDIS_CONTAINER=${JSON.stringify(redis)}`);
  }
  return lines.length === 1 && !missing.has('nginx') ? '' : lines.join('\n');
}

export function collectorLastSuccessAt(status: CollectorStatusView): string | null {
  return normalizedTime(status.last_success_at);
}
