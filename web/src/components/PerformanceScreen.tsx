import { useCallback, useMemo, useState } from 'react';
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
import { DetailsPanel, PanelError } from './performance/PerformancePanel.tsx';
import CollectorStatus from './performance/CollectorStatus.tsx';
import CorrelationTimeline from './performance/CorrelationTimeline.tsx';
import DependencyPanels from './performance/DependencyPanels.tsx';
import PerformanceSummary, {
  PerformanceSummarySkeleton,
} from './performance/PerformanceSummary.tsx';
import RouteTable from './performance/RouteTable.tsx';
import {
  fetchCollectorStatuses,
  fetchFleetContainers,
  fetchPerformancePostgres,
  fetchPerformanceRedis,
  fetchPerformanceRoutes,
  fetchPerformanceSummary,
  fetchPerformanceTimeline,
  type PerformanceFilters,
  type ServiceView,
} from '@/lib/api.ts';
import { collectorState, performanceDrilldownHref } from '@/lib/performance.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

const ranges = [
  { hours: 1, label: '1h' },
  { hours: 6, label: '6h' },
  { hours: 24, label: '24h' },
  { hours: 168, label: '7d' },
] as const;

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

export default function PerformanceScreen({
  services,
  onUnauthorized,
  canFleet,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
  // Whether the user may see fleet-wide numbers and collector status.
  // A user scoped to some services may not: they always look at one of
  // their services, and the fleet calls are never made.
  canFleet: boolean;
}) {
  const [hours, setHours] = useState(1);
  const [chosenServiceId, setServiceId] = useState('all');
  const serviceId =
    canFleet || services.some((service) => service.id === chosenServiceId)
      ? chosenServiceId
      : (services[0]?.id ?? '');
  const [refresh, setRefresh] = useState(0);
  const [timelineOpen, setTimelineOpen] = useState(false);
  const [routesOpen, setRoutesOpen] = useState(false);
  const [dependenciesOpen, setDependenciesOpen] = useState(false);
  const [discoveryOpen, setDiscoveryOpen] = useState(false);

  const filters = useMemo(() => filtersFor(hours, serviceId), [hours, refresh, serviceId]);
  const scope = `${serviceId}:${hours}`;
  const hasScope = serviceId !== '';
  const readSummary = useCallback((signal: AbortSignal) => fetchPerformanceSummary(filters, signal), [filters]);
  const readTimeline = useCallback((signal: AbortSignal) => fetchPerformanceTimeline(filters, signal), [filters]);
  const readRoutes = useCallback((signal: AbortSignal) => fetchPerformanceRoutes(filters, signal), [filters]);
  const readPostgres = useCallback((signal: AbortSignal) => fetchPerformancePostgres(filters, signal), [filters]);
  const readRedis = useCallback((signal: AbortSignal) => fetchPerformanceRedis(filters, signal), [filters]);

  const summary = usePanelRead(scope, hasScope ? readSummary : null, refresh, onUnauthorized);
  const timeline = usePanelRead(scope, hasScope && timelineOpen ? readTimeline : null, refresh, onUnauthorized);
  const routes = usePanelRead(scope, hasScope && routesOpen ? readRoutes : null, refresh, onUnauthorized);
  const postgres = usePanelRead(scope, hasScope && dependenciesOpen ? readPostgres : null, refresh, onUnauthorized);
  const redis = usePanelRead(scope, hasScope && dependenciesOpen ? readRedis : null, refresh, onUnauthorized);
  const collectors = usePanelRead('fleet', canFleet ? fetchCollectorStatuses : null, refresh, onUnauthorized);
  const discovery = usePanelRead('fleet', canFleet && discoveryOpen ? fetchFleetContainers : null, refresh, onUnauthorized);

  const refreshing = summary.loading || collectors.loading;
  const collectorWarnings = (collectors.data ?? []).filter((collector) => {
    const state = collectorState(collector);
    return state === 'degraded' || state === 'stale';
  });

  const drilldownFilters: PerformanceFilters = {
    serviceId: serviceId === 'all' ? undefined : serviceId,
    start: summary.data?.start,
    end: summary.data?.end,
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
              {canFleet && <SelectItem value="all">All services</SelectItem>}
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
            onClick={() => setRefresh((value) => value + 1)}
          >
            <RefreshCw className={refreshing ? 'animate-spin' : ''} aria-hidden />
            {refreshing ? 'Refreshing' : 'Refresh'}
          </Button>
        </div>
      </div>

      <PanelError name="performance summary" error={summary.error} retry={summary.retry} />
      {summary.data !== null ? (
        <PerformanceSummary summary={summary.data} routes={routes.data} />
      ) : summary.loading ? <PerformanceSummarySkeleton /> : null}

      {collectorWarnings.length > 0 && (
        <Alert>
          <TriangleAlert aria-hidden />
          <AlertTitle>Collector attention needed</AlertTitle>
          <AlertDescription>
            {collectorWarnings.map(({ source }) => source.replace(/_/g, ' ')).join(', ')}.
            See the last successful sample and next step below.
          </AlertDescription>
        </Alert>
      )}
      <PanelError name="collector status" error={collectors.error} retry={collectors.retry} />
      {collectors.loading && collectors.data === null && (
        <p className="text-muted-foreground text-sm" role="status">Loading collector status…</p>
      )}
      {collectors.data !== null && (
        <CollectorStatus collectors={collectors.data} services={services} serviceId={serviceId} fleetContainers={[]} />
      )}

      <DetailsPanel title="Timeline" description="Compare latency, deploys, and runtime events" onChange={setTimelineOpen}>
        <PanelError name="timeline" error={timeline.error} retry={timeline.retry} />
        {timeline.loading && timeline.data === null && <p role="status">Loading timeline…</p>}
        {timeline.data !== null && <CorrelationTimeline timeline={timeline.data} />}
      </DetailsPanel>
      <DetailsPanel title="HTTP routes" description="Find the slowest approved route families" onChange={setRoutesOpen}>
        <PanelError name="HTTP routes" error={routes.error} retry={routes.retry} />
        {routes.loading && routes.data === null && <p role="status">Loading routes…</p>}
        {routes.data !== null && <RouteTable routes={routes.data} />}
      </DetailsPanel>
      <DetailsPanel title="Database signals" description="Review PostgreSQL and Redis samples" onChange={setDependenciesOpen}>
        <PanelError name="PostgreSQL" error={postgres.error} retry={postgres.retry} />
        <PanelError name="Redis" error={redis.error} retry={redis.retry} />
        {((postgres.loading && postgres.data === null) || (redis.loading && redis.data === null)) && (
          <p role="status">Loading database signals…</p>
        )}
        {(postgres.data !== null || redis.data !== null) && (
          <DependencyPanels postgres={postgres.data ?? []} redis={redis.data ?? []} />
        )}
      </DetailsPanel>

      {summary.data !== null && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Search className="h-4 w-4" aria-hidden />
              Investigation feed
            </CardTitle>
            <CardDescription>Ranked correlations and the next safe drill-down.</CardDescription>
          </CardHeader>
          <CardContent>
            {summary.data.anomalies.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No notable change against the preceding window.
              </p>
            ) : (
              <div className="space-y-2">
                {summary.data.anomalies.map((anomaly, index) => (
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

      {canFleet && (
        <DetailsPanel title="Discover collector setup" description="Find Docker database sources" onChange={setDiscoveryOpen}>
          <PanelError name="discovered sources" error={discovery.error} retry={discovery.retry} />
          {discovery.loading && discovery.data === null && <p role="status">Loading discovered sources…</p>}
          {discovery.data !== null && (
            <CollectorStatus
              collectors={collectors.data ?? []}
              services={services}
              serviceId={serviceId}
              fleetContainers={discovery.data.containers}
              setupOnly
            />
          )}
        </DetailsPanel>
      )}
    </div>
  );
}
