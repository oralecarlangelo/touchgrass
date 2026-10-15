import { useCallback, useEffect, useMemo, useState } from 'react';
import { RefreshCw, Search, TriangleAlert } from 'lucide-react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from '../ErrorState.tsx';
import CorrelationTimeline from '../performance/CorrelationTimeline.tsx';
import DependencyPanels from '../performance/DependencyPanels.tsx';
import PerformanceSummary, {
  PerformanceSummarySkeleton,
} from '../performance/PerformanceSummary.tsx';
import RouteTable from '../performance/RouteTable.tsx';
import {
  fetchMetrics,
  fetchPerformancePostgres,
  fetchPerformanceRedis,
  fetchPerformanceRoutes,
  fetchPerformanceSummary,
  fetchPerformanceTimeline,
  isUnauthorized,
  type Metric,
  type PerformanceFilters,
  type PerformanceRouteRow,
  type PerformanceSummaryView,
  type PerformanceTimelineView,
  type PostgresPerformanceRow,
  type RedisPerformanceRow,
} from '@/lib/api.ts';
import { formatBytes, formatDuration } from '@/lib/format.ts';
import {
  performanceDrilldownHref,
  servicePerformanceState,
} from '@/lib/performance.ts';

const seriesConfig = {
  cpu: { label: 'CPU %', color: 'var(--chart-1)' },
  mem: { label: 'Mem %', color: 'var(--chart-2)' },
} satisfies ChartConfig;

const ranges = [
  { hours: 1, label: '1h' },
  { hours: 6, label: '6h' },
  { hours: 24, label: '24h' },
] as const;

interface ServicePerformanceData {
  metrics: Metric[] | null;
  summary: PerformanceSummaryView | null;
  timeline: PerformanceTimelineView | null;
  routes: PerformanceRouteRow[] | null;
  postgres: PostgresPerformanceRow[] | null;
  redis: RedisPerformanceRow[] | null;
}

const emptyData: ServicePerformanceData = {
  metrics: null,
  summary: null,
  timeline: null,
  routes: null,
  postgres: null,
  redis: null,
};

function latestByContainer(metrics: Metric[]): Metric[] {
  const latest = new Map<string, Metric>();

  for (const metric of metrics) {
    const current = latest.get(metric.container_name);

    if (current === undefined || metric.sampled_at > current.sampled_at) {
      latest.set(metric.container_name, metric);
    }
  }

  return [...latest.values()];
}

function memPercent(metric: Metric): number | null {
  if (metric.mem_limit === 0) {
    return null;
  }

  return (metric.mem_bytes / metric.mem_limit) * 100;
}

function shortTime(value: string): string {
  const parsed = new Date(value);

  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function selectedFilters(serviceId: string, hours: number): PerformanceFilters {
  const end = new Date();
  const start = new Date(end.getTime() - hours * 60 * 60 * 1000);

  return {
    serviceId,
    start: start.toISOString(),
    end: end.toISOString(),
    resolution: 'auto',
    sort: 'p95_ms',
    limit: 50,
  };
}

function ContainerMetrics({ metrics }: { metrics: Metric[] | null }) {
  const [container, setContainer] = useState('all');
  const containers = useMemo(
    () => [...new Set((metrics ?? []).map((metric) => metric.container_name))].sort(),
    [metrics],
  );
  const series = useMemo(() => {
    const rows = (metrics ?? [])
      .filter((metric) => container === 'all' || metric.container_name === container)
      .sort((left, right) => left.sampled_at.localeCompare(right.sampled_at));

    if (container !== 'all') {
      return rows.map((metric) => ({
        at: shortTime(metric.sampled_at),
        cpu: Number(metric.cpu_percent.toFixed(1)),
        mem: memPercent(metric) === null ? null : Number((memPercent(metric) ?? 0).toFixed(1)),
      }));
    }

    const buckets = new Map<string, { cpu: number; mem: number; cpuCount: number; memCount: number }>();

    for (const metric of rows) {
      const bucket = buckets.get(metric.sampled_at) ?? {
        cpu: 0,
        mem: 0,
        cpuCount: 0,
        memCount: 0,
      };
      bucket.cpu += metric.cpu_percent;
      bucket.cpuCount += 1;
      const memory = memPercent(metric);
      if (memory !== null) {
        bucket.mem += memory;
        bucket.memCount += 1;
      }
      buckets.set(metric.sampled_at, bucket);
    }

    return [...buckets.entries()]
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([at, bucket]) => ({
        at: shortTime(at),
        cpu: Number((bucket.cpu / bucket.cpuCount).toFixed(1)),
        mem: bucket.memCount === 0 ? null : Number((bucket.mem / bucket.memCount).toFixed(1)),
      }));
  }, [metrics, container]);
  const latest = useMemo(() => latestByContainer(metrics ?? []), [metrics]);

  return (
    <div className="grid gap-4 xl:grid-cols-[1.1fr_0.9fr]">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between gap-3">
          <div>
            <CardTitle className="text-sm font-medium">Container CPU / memory</CardTitle>
            <CardDescription>Existing 30-second runtime samples.</CardDescription>
          </div>
          <Select value={container} onValueChange={setContainer}>
            <SelectTrigger className="w-48" aria-label="Container filter">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">all containers (avg)</SelectItem>
              {containers.map((name) => (
                <SelectItem key={name} value={name}>
                  {name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </CardHeader>
        <CardContent>
          {metrics === null ? (
            <Skeleton className="h-56 w-full" />
          ) : series.length === 0 ? (
            <p className="text-muted-foreground text-sm">No container samples yet.</p>
          ) : (
            <ChartContainer config={seriesConfig} className="h-56 w-full">
              <LineChart data={series} accessibilityLayer>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
                <YAxis tickLine={false} axisLine={false} width={40} />
                <ChartTooltip content={<ChartTooltipContent />} />
                <Line dataKey="cpu" stroke="var(--color-cpu)" dot={false} connectNulls={false} />
                <Line dataKey="mem" stroke="var(--color-mem)" dot={false} connectNulls={false} />
              </LineChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">Latest container samples</CardTitle>
        </CardHeader>
        <CardContent>
          {metrics === null ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : latest.length === 0 ? (
            <p className="text-muted-foreground text-sm">No samples yet.</p>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Container</TableHead>
                    <TableHead className="text-right">CPU</TableHead>
                    <TableHead className="text-right">Memory</TableHead>
                    <TableHead className="text-right">Restarts</TableHead>
                    <TableHead className="text-right">Uptime</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {latest.map((sample) => {
                    const memory = memPercent(sample);

                    return (
                      <TableRow key={sample.container_name}>
                        <TableCell className="font-medium">{sample.container_name}</TableCell>
                        <TableCell className="text-right font-mono text-xs">
                          {sample.cpu_percent.toFixed(1)}%
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs">
                          {formatBytes(sample.mem_bytes)}
                          {memory !== null && ` (${memory.toFixed(1)}%)`}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs">
                          {sample.restarts > 0 ? (
                            <Badge variant="destructive">{sample.restarts}</Badge>
                          ) : (
                            sample.restarts
                          )}
                        </TableCell>
                        <TableCell className="text-right font-mono text-xs">
                          {formatDuration(sample.uptime_secs)}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default function PerformanceTab({
  serviceId,
  onUnauthorized,
}: {
  serviceId: string;
  onUnauthorized: () => void;
}) {
  const [hours, setHours] = useState(1);
  const [data, setData] = useState<ServicePerformanceData>(emptyData);
  const [failures, setFailures] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(
    async (background = false) => {
      if (background) {
        setRefreshing(true);
      } else {
        setLoading(true);
      }

      const filters = selectedFilters(serviceId, hours);
      const results = await Promise.allSettled([
        fetchMetrics(serviceId),
        fetchPerformanceSummary(filters),
        fetchPerformanceTimeline(filters),
        fetchPerformanceRoutes(filters),
        fetchPerformancePostgres(filters),
        fetchPerformanceRedis(filters),
      ]);

      if (
        results.some((result) => result.status === 'rejected' && isUnauthorized(result.reason))
      ) {
        onUnauthorized();
        return;
      }

      setFailures(
        results.flatMap((result) =>
          result.status === 'rejected'
            ? [result.reason instanceof Error ? result.reason.message : String(result.reason)]
            : [],
        ),
      );
      setData({
        metrics: results[0].status === 'fulfilled' ? results[0].value : null,
        summary: results[1].status === 'fulfilled' ? results[1].value : null,
        timeline: results[2].status === 'fulfilled' ? results[2].value : null,
        routes: results[3].status === 'fulfilled' ? results[3].value : null,
        postgres: results[4].status === 'fulfilled' ? results[4].value : null,
        redis: results[5].status === 'fulfilled' ? results[5].value : null,
      });
      setLoading(false);
      setRefreshing(false);
    },
    [hours, onUnauthorized, serviceId],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const signalCount =
    (data.summary?.request_count ?? 0) +
    (data.timeline?.points.length ?? 0) +
    (data.routes?.length ?? 0) +
    (data.postgres?.length ?? 0) +
    (data.redis?.length ?? 0);
  const state = servicePerformanceState({
    serviceId,
    loading,
    metricCount: data.metrics?.length ?? 0,
    signalCount,
    failureCount: failures.length,
  });
  const linkFilters: PerformanceFilters = {
    serviceId,
    start: data.summary?.start,
    end: data.summary?.end,
  };

  if (state === 'unknown-service') {
    return (
      <ErrorState
        title="Service unavailable"
        message="This performance view needs a known service. Return to Services and select it again."
        onRetry={() => window.location.assign('#/services')}
      />
    );
  }

  if (state === 'error') {
    return (
      <ErrorState
        title="Couldn't load service performance"
        message={failures[0] ?? 'No performance source responded.'}
        onRetry={() => void load()}
      />
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
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

      {failures.length > 0 && (
        <Alert>
          <TriangleAlert aria-hidden />
          <AlertTitle>
            {state === 'container-only' ? 'Container metrics only' : 'Some signals are unavailable'}
          </AlertTitle>
          <AlertDescription>
            Available runtime history remains visible. {failures[0]}
          </AlertDescription>
        </Alert>
      )}

      {loading ? (
        <PerformanceSummarySkeleton />
      ) : data.summary !== null ? (
        <PerformanceSummary summary={data.summary} routes={data.routes ?? []} />
      ) : null}

      <ContainerMetrics metrics={data.metrics} />

      {data.timeline !== null && <CorrelationTimeline timeline={data.timeline} />}
      {data.routes !== null && <RouteTable routes={data.routes} />}
      {(data.postgres !== null || data.redis !== null) && (
        <DependencyPanels postgres={data.postgres ?? []} redis={data.redis ?? []} />
      )}

      {data.summary !== null && data.summary.anomalies.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-sm">
              <Search className="h-4 w-4" aria-hidden />
              Related investigation
            </CardTitle>
            <CardDescription>Correlations for this service and selected window.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            {data.summary.anomalies.map((anomaly, index) => (
              <div key={`${anomaly.kind}:${index}`} className="rounded-md border p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <Badge variant="secondary">{anomaly.kind}</Badge>
                    <p className="mt-2 text-sm">{anomaly.summary}</p>
                  </div>
                  <div className="flex gap-2">
                    <Button asChild size="sm" variant="outline">
                      <a href={performanceDrilldownHref('logs', linkFilters)}>Logs</a>
                    </Button>
                    <Button asChild size="sm" variant="outline">
                      <a href={performanceDrilldownHref('issues', linkFilters)}>Issues</a>
                    </Button>
                    <Button asChild size="sm" variant="outline">
                      <a href={performanceDrilldownHref('audit', linkFilters)}>Deploy history</a>
                    </Button>
                  </div>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
