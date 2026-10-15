import { useCallback, useEffect, useMemo, useState } from 'react';
import { RefreshCw, Search, TriangleAlert } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import ErrorState from './ErrorState.tsx';
import CollectorStatus from './performance/CollectorStatus.tsx';
import CorrelationTimeline from './performance/CorrelationTimeline.tsx';
import DependencyPanels from './performance/DependencyPanels.tsx';
import PerformanceSummary, {
  PerformanceSummarySkeleton,
} from './performance/PerformanceSummary.tsx';
import RouteTable from './performance/RouteTable.tsx';
import {
  fetchCollectorStatuses,
  fetchPerformancePostgres,
  fetchPerformanceRedis,
  fetchPerformanceRoutes,
  fetchPerformanceSummary,
  fetchPerformanceTimeline,
  isUnauthorized,
  type CollectorStatusView,
  type PerformanceFilters,
  type PerformanceRouteRow,
  type PerformanceSummaryView,
  type PerformanceTimelineView,
  type PostgresPerformanceRow,
  type RedisPerformanceRow,
  type ServiceView,
} from '@/lib/api.ts';
import { collectorState, performanceDrilldownHref } from '@/lib/performance.ts';

const ranges = [
  { hours: 1, label: '1h' },
  { hours: 6, label: '6h' },
  { hours: 24, label: '24h' },
  { hours: 168, label: '7d' },
] as const;

interface PerformanceData {
  summary: PerformanceSummaryView | null;
  timeline: PerformanceTimelineView | null;
  routes: PerformanceRouteRow[] | null;
  postgres: PostgresPerformanceRow[] | null;
  redis: RedisPerformanceRow[] | null;
  collectors: CollectorStatusView[] | null;
}

const emptyData: PerformanceData = {
  summary: null,
  timeline: null,
  routes: null,
  postgres: null,
  redis: null,
  collectors: null,
};

function filtersFor(hours: number, serviceId: string): PerformanceFilters {
  const end = new Date();
  const start = new Date(end.getTime() - hours * 60 * 60 * 1000);

  return {
    serviceId: serviceId === 'all' ? undefined : serviceId,
    start: start.toISOString(),
    end: end.toISOString(),
    resolution: 'auto',
    sort: 'p95_ms',
    limit: 50,
  };
}

function rejectedMessage(reason: unknown): string {
  return reason instanceof Error ? reason.message : String(reason);
}

export default function PerformanceScreen({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [hours, setHours] = useState(1);
  const [serviceId, setServiceId] = useState('all');
  const [data, setData] = useState<PerformanceData>(emptyData);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [failures, setFailures] = useState<string[]>([]);

  const load = useCallback(
    async (background = false) => {
      if (background) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }
      setFailures([]);

      const filters = filtersFor(hours, serviceId);
      const results = await Promise.allSettled([
        fetchPerformanceSummary(filters),
        fetchPerformanceTimeline(filters),
        fetchPerformanceRoutes(filters),
        fetchPerformancePostgres(filters),
        fetchPerformanceRedis(filters),
        fetchCollectorStatuses(),
      ]);

      const unauthorized = results.some(
        (result) => result.status === 'rejected' && isUnauthorized(result.reason),
      );

      if (unauthorized) {
        onUnauthorized();
        return;
      }

      const errors = results.flatMap((result) =>
        result.status === 'rejected' ? [rejectedMessage(result.reason)] : [],
      );
      setFailures([...new Set(errors)]);
      setData({
        summary: results[0].status === 'fulfilled' ? results[0].value : null,
        timeline: results[1].status === 'fulfilled' ? results[1].value : null,
        routes: results[2].status === 'fulfilled' ? results[2].value : null,
        postgres: results[3].status === 'fulfilled' ? results[3].value : null,
        redis: results[4].status === 'fulfilled' ? results[4].value : null,
        collectors: results[5].status === 'fulfilled' ? results[5].value : null,
      });
      setLoading(false);
      setRefreshing(false);
    },
    [hours, serviceId, onUnauthorized],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const failedCompletely = !loading && Object.values(data).every((value) => value === null);
  const collectorWarnings = useMemo(
    () =>
      (data.collectors ?? []).filter((collector) => collectorState(collector) !== 'healthy'),
    [data.collectors],
  );
  const noSignals =
    data.summary?.request_count === 0 &&
    (data.timeline?.points.length ?? 0) === 0 &&
    (data.routes?.length ?? 0) === 0 &&
    (data.postgres?.length ?? 0) === 0 &&
    (data.redis?.length ?? 0) === 0;
  const drilldownFilters: PerformanceFilters = {
    serviceId: serviceId === 'all' ? undefined : serviceId,
    start: data.summary?.start,
    end: data.summary?.end,
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-col justify-between gap-3 md:flex-row md:items-start">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-lg font-semibold tracking-tight">Performance</h1>
            <Badge variant="outline">agentless</Badge>
          </div>
          <p className="text-muted-foreground mt-1 max-w-2xl text-sm">
            Align latency, traffic, resources, dependencies, logs, and runtime changes. Signals
            correlate in time; they do not prove causation.
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2" aria-label="Performance filters">
          <Select value={serviceId} onValueChange={setServiceId}>
            <SelectTrigger className="w-44" aria-label="Filter by service">
              <SelectValue placeholder="All services" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All services</SelectItem>
              {services.map((service) => (
                <SelectItem key={service.id} value={service.id}>
                  {service.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex rounded-md border p-0.5" role="group" aria-label="Time range">
            {ranges.map((range) => (
              <Button
                key={range.hours}
                type="button"
                size="sm"
                variant={hours === range.hours ? 'secondary' : 'ghost'}
                aria-pressed={hours === range.hours}
                onClick={() => setHours(range.hours)}
              >
                {range.label}
              </Button>
            ))}
          </div>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={refreshing}
            onClick={() => void load(true)}
          >
            <RefreshCw className={refreshing ? 'animate-spin' : ''} aria-hidden />
            {refreshing ? 'Refreshing' : 'Refresh'}
          </Button>
        </div>
      </div>

      {failedCompletely && (
        <ErrorState
          title="Couldn't load performance signals"
          message={failures[0] ?? 'No performance endpoint responded.'}
          onRetry={() => void load()}
        />
      )}

      {!failedCompletely && failures.length > 0 && (
        <Alert>
          <TriangleAlert aria-hidden />
          <AlertTitle>Some signals are unavailable</AlertTitle>
          <AlertDescription>
            Available panels remain usable. Retry after checking collector status. {failures[0]}
          </AlertDescription>
        </Alert>
      )}

      {!failedCompletely && collectorWarnings.length > 0 && (
        <Alert>
          <TriangleAlert aria-hidden />
          <AlertTitle>Collector attention needed</AlertTitle>
          <AlertDescription>
            {collectorWarnings.length} source{collectorWarnings.length === 1 ? ' is' : 's are'} stale,
            degraded, or awaiting setup. Historical data remains visible.
          </AlertDescription>
        </Alert>
      )}

      {loading ? (
        <PerformanceSummarySkeleton />
      ) : data.summary !== null ? (
        <PerformanceSummary summary={data.summary} routes={data.routes ?? []} />
      ) : null}

      {!loading && noSignals && !failedCompletely && (
        <div className="rounded-lg border border-dashed px-6 py-10 text-center">
          <p className="text-sm font-medium">No performance samples yet</p>
          <p className="text-muted-foreground mx-auto mt-1 max-w-lg text-sm">
            Configure the read-only Nginx timing log and optional PostgreSQL or Redis collectors.
            Touch Grass will keep each available source independent.
          </p>
        </div>
      )}

      {data.timeline !== null && <CorrelationTimeline timeline={data.timeline} />}
      {data.routes !== null && <RouteTable routes={data.routes} />}
      {(data.postgres !== null || data.redis !== null) && (
        <DependencyPanels postgres={data.postgres ?? []} redis={data.redis ?? []} />
      )}

      {data.summary !== null && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Search className="h-4 w-4" aria-hidden />
              Investigation feed
            </CardTitle>
            <CardDescription>Ranked correlations and the next safe drill-down.</CardDescription>
          </CardHeader>
          <CardContent>
            {data.summary.anomalies.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No notable change against the preceding window.
              </p>
            ) : (
              <div className="space-y-2">
                {data.summary.anomalies.map((anomaly, index) => (
                  <div
                    key={`${anomaly.kind}:${index}`}
                    className="flex flex-col justify-between gap-3 rounded-md border p-3 sm:flex-row sm:items-center"
                  >
                    <div>
                      <div className="flex items-center gap-2">
                        <Badge variant="secondary">{anomaly.kind}</Badge>
                        <span className="text-muted-foreground text-xs">
                          score {anomaly.score.toFixed(1)}
                        </span>
                      </div>
                      <p className="mt-2 text-sm">{anomaly.summary}</p>
                    </div>
                    <div className="flex gap-2">
                      <Button asChild size="sm" variant="outline">
                        <a href={performanceDrilldownHref('logs', drilldownFilters)}>Open logs</a>
                      </Button>
                      <Button asChild size="sm" variant="outline">
                        <a href={performanceDrilldownHref('issues', drilldownFilters)}>Open issues</a>
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {data.collectors !== null && <CollectorStatus collectors={data.collectors} />}
    </div>
  );
}
