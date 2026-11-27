import { Fragment, lazy, Suspense, useEffect, useRef, useState } from 'react';

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
import { Input } from '@/components/ui/input';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from './ErrorState.tsx';
import HeartbeatStatus from './HeartbeatStatus.tsx';
import PromoteDialog from './PromoteDialog.tsx';
import ScreenErrorBoundary from './ScreenErrorBoundary.tsx';
import {
  fetchFleetContainerPage,
  fetchSystem,
  fetchSystemHistory,
  isUnauthorized,
  type ContainerFreshness,
  type FleetContainer,
  type HistoryPoint,
  type SystemSnapshot,
} from '@/lib/api.ts';
import { formatBytes, formatDuration, timeAgo } from '@/lib/format.ts';
import { inventoryHeartbeat, snapshotHeartbeat } from '@/lib/heartbeat.ts';

const FleetHistoryCharts = lazy(() => import('./FleetHistoryCharts.tsx'));

const refreshIntervalMs = 30_000;
const containerPageSize = 40;

const rangeOptions = [
  { hours: 1, label: '1h' },
  { hours: 6, label: '6h' },
  { hours: 24, label: '24h' },
  { hours: 168, label: '7d' },
];

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4 py-1.5 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-mono text-xs">{value}</span>
    </div>
  );
}

function UsageBar({
  label,
  used,
  total,
}: {
  label: string;
  used: number | null;
  total: number | null;
}) {
  if (used === null || total === null || total <= 0) {
    return (
      <div className="py-1.5">
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">{label}</span>
          <span className="font-mono text-xs">n/a</span>
        </div>
      </div>
    );
  }

  const percent = Math.min(100, Math.max(0, (used / total) * 100));

  return (
    <div className="py-1.5">
      <div className="flex items-center justify-between text-sm">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-mono text-xs">
          {formatBytes(used)} / {formatBytes(total)} ({percent.toFixed(0)}%)
        </span>
      </div>
      <div
        className="bg-muted mt-1 h-2 overflow-hidden rounded-full"
        role="progressbar"
        aria-valuenow={Math.round(percent)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${label} usage`}
      >
        <div className="bg-primary h-full rounded-full" style={{ width: `${percent}%` }} />
      </div>
    </div>
  );
}

function ManagedBadge({
  container,
  onSelectService,
}: {
  container: FleetContainer;
  onSelectService: (id: string) => void;
}) {
  if (!container.managed) {
    return <Badge variant="secondary">unmanaged</Badge>;
  }

  const serviceId = container.service_id;

  if (serviceId === undefined || serviceId === null || serviceId === '') {
    return <Badge variant="default">managed</Badge>;
  }

  return (
    <a
      href={`#/services/${encodeURIComponent(serviceId)}`}
      onClick={(event) => {
        event.preventDefault();
        onSelectService(serviceId);
      }}
    >
      <Badge variant="default">managed</Badge>
    </a>
  );
}

function memPercent(container: FleetContainer): number | null {
  if (container.mem_bytes === null || container.mem_limit === null || container.mem_limit <= 0) {
    return null;
  }

  return (container.mem_bytes / container.mem_limit) * 100;
}

const containerFilters = ['all', 'running', 'stopped', 'unknown', 'risky'] as const;

interface ContainerPage {
  scope: string;
  rows: FleetContainer[];
  freshness: ContainerFreshness;
  hasNext: boolean;
}

export default function FleetScreen({
  onUnauthorized,
  onSelectService,
  onSelectContainer,
  isAdmin,
}: {
  onUnauthorized: () => void;
  onSelectService: (id: string) => void;
  onSelectContainer: (id: string) => void;
  isAdmin: boolean;
}) {
  const [system, setSystem] = useState<SystemSnapshot | null>(null);
  const [containerPage, setContainerPage] = useState<ContainerPage | null>(null);
  const [topCPU, setTopCPU] = useState<FleetContainer[] | null>(null);
  const [systemError, setSystemError] = useState<string | null>(null);
  const [containerFailure, setContainerFailure] = useState<{ scope: string; message: string } | null>(null);
  const [range, setRange] = useState(24);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [cpu, setCpu] = useState<HistoryPoint[] | null>(null);
  const [mem, setMem] = useState<HistoryPoint[] | null>(null);
  const [loadAvg, setLoadAvg] = useState<HistoryPoint[] | null>(null);
  const [loadedRange, setLoadedRange] = useState<number | null>(null);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [promoteTarget, setPromoteTarget] = useState<string | null>(null);
  const promoteTrigger = useRef<HTMLButtonElement | null>(null);
  const [query, setQuery] = useState('');
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<(typeof containerFilters)[number]>('all');
  const [sort, setSort] = useState('cpu');
  const [page, setPage] = useState(0);
  const [systemAttempt, setSystemAttempt] = useState(0);
  const [containersAttempt, setContainersAttempt] = useState(0);
  const [historyAttempt, setHistoryAttempt] = useState(0);
  const pageScope = JSON.stringify([page, filter, search, sort]);
  const containers = containerPage?.scope === pageScope ? containerPage.rows : null;
  const freshness = containerPage?.freshness ?? null;
  const containersError = containerFailure?.scope === pageScope ? containerFailure.message : null;
  const showingTopPage = page === 0 && search === '' && filter === 'all' && sort === 'cpu';

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let running = false;
    const load = async () => {
      if (running || document.hidden || controller.signal.aborted) return;
      if (timer !== undefined) clearTimeout(timer);
      running = true;
      try {
        const snapshot = await fetchSystem(controller.signal);
        if (!controller.signal.aborted) {
          setSystem(snapshot);
          setSystemError(null);
        }
      } catch (err) {
        if (controller.signal.aborted) return;
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }
        setSystemError(err instanceof Error ? err.message : String(err));
      } finally {
        running = false;
        if (!controller.signal.aborted) timer = setTimeout(() => void load(), refreshIntervalMs);
      }
    };
    void load();
    const onVisible = () => { if (!document.hidden) void load(); };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      controller.abort();
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [onUnauthorized, systemAttempt]);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let running = false;
    const load = async () => {
      if (running || document.hidden || controller.signal.aborted) return;
      if (timer !== undefined) clearTimeout(timer);
      running = true;
      try {
        const fleet = await fetchFleetContainerPage({
          offset: page * containerPageSize, limit: containerPageSize + 1, sort, filter, search,
        }, controller.signal);
        if (!controller.signal.aborted) {
          setContainerPage({
            scope: pageScope, rows: fleet.containers.slice(0, containerPageSize), freshness: fleet.freshness,
            hasNext: fleet.containers.length > containerPageSize,
          });
          if (showingTopPage) setTopCPU(fleet.containers.slice(0, 5).filter((container) => (container.cpu_percent ?? 0) > 0));
          setContainerFailure(null);
        }
      } catch (err) {
        if (controller.signal.aborted) return;
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }
        setContainerFailure({ scope: pageScope, message: err instanceof Error ? err.message : String(err) });
      } finally {
        running = false;
        if (page === 0 && !controller.signal.aborted) timer = setTimeout(() => void load(), refreshIntervalMs);
      }
    };
    void load();
    const onVisible = () => { if (!document.hidden) void load(); };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      controller.abort();
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [containersAttempt, filter, onUnauthorized, page, pageScope, search, showingTopPage, sort]);

  useEffect(() => {
    if (!historyOpen) return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let running = false;
    const load = async () => {
      if (running || document.hidden || controller.signal.aborted) return;
      if (timer !== undefined) clearTimeout(timer);
      running = true;
      try {
        const history = await fetchSystemHistory(range, controller.signal);
        if (!controller.signal.aborted) {
          setCpu(history.cpu);
          setMem(history.mem);
          setLoadAvg(history.load);
          setLoadedRange(range);
          setHistoryError(null);
        }
      } catch (err) {
        if (controller.signal.aborted) return;
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }
        setHistoryError(err instanceof Error ? err.message : String(err));
      } finally {
        running = false;
        if (!controller.signal.aborted) timer = setTimeout(() => void load(), refreshIntervalMs);
      }
    };
    void load();
    const onVisible = () => { if (!document.hidden) void load(); };
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      controller.abort();
      if (timer !== undefined) clearTimeout(timer);
    };
  }, [historyAttempt, historyOpen, onUnauthorized, range]);

  const historyLoading = loadedRange !== range || cpu === null || mem === null || loadAvg === null;

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold tracking-tight">Fleet</h1>

      {systemError !== null && system === null ? (
        <ErrorState
          title="Couldn't load host info"
          message={systemError}
          onRetry={() => setSystemAttempt((attempt) => attempt + 1)}
        />
      ) : system === null ? (
        <div className="grid gap-4 lg:grid-cols-3">
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
        </div>
      ) : (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-sm font-medium">
                Host resources (auto-refreshes every 30s)
              </CardTitle>
            </CardHeader>
            <CardContent className="divide-y divide-border">
              {systemError !== null && (
                <div className="mb-2 flex items-center justify-between gap-2 pb-2 text-sm">
                  <span className="text-muted-foreground">
                    Refresh failed: {systemError} — showing last good data.
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setSystemAttempt((attempt) => attempt + 1)}
                  >
                    Retry
                  </Button>
                </div>
              )}
              <div className="pb-2">
                <HeartbeatStatus
                  label="Host heartbeat"
                  state={snapshotHeartbeat(system.freshness)}
                  observedAt={system.freshness.observed_at}
                  timeLabel={system.freshness.complete ? 'Last successful sample' : 'Last attempt'}
                  detail={system.freshness.last_error_category === 'collection_failed'
                    ? 'Host metrics can still update. Check Docker access for daemon metrics.'
                    : undefined}
                />
              </div>
              <Row
                label="CPU"
                value={system.cpu_percent === null ? 'n/a' : `${system.cpu_percent.toFixed(1)}%`}
              />
              <UsageBar
                label="Memory"
                used={system.mem_used_bytes}
                total={system.mem_total_bytes}
              />
              <UsageBar
                label="Disk (root)"
                used={system.disk_used_bytes}
                total={system.disk_total_bytes}
              />
            </CardContent>
          </Card>
        </>
      )}

      {topCPU !== null && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Top container CPU</CardTitle>
            <CardDescription>
              {showingTopPage ? 'Latest available samples.' : 'Last samples from the unfiltered CPU view.'}
              {' '}100% represents one CPU core.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <ul className="divide-y">
              {topCPU.map((container) => (
                  <li key={container.id} className="flex justify-between gap-3 py-1.5 text-sm">
                    <button type="button" className="min-w-0 truncate text-left underline-offset-4 hover:underline"
                      onClick={() => onSelectContainer(container.id)}>{container.name}</button>
                    <span className="shrink-0 font-mono">{container.cpu_percent?.toFixed(1)}%</span>
                  </li>
                ))}
            </ul>
            {topCPU.length === 0 && (
              <p className="text-muted-foreground text-sm">No container CPU usage sampled.</p>
            )}
          </CardContent>
        </Card>
      )}

          <Card>
            <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-4">
              <div className="space-y-1.5">
                <CardTitle className="text-sm font-medium">
                  Containers
                </CardTitle>
                {containers !== null && (
                  <CardDescription>
                    {freshness?.observed_count ?? 0} seen in the last inventory
                  </CardDescription>
                )}
              </div>
              <form className="flex min-w-0 gap-2" onSubmit={(event) => {
                event.preventDefault();
                setSearch(query.trim());
                setPage(0);
              }}>
                <Input
                  placeholder="Name, project or ID"
                  value={query}
                  maxLength={128}
                  onChange={(event) => setQuery(event.target.value)}
                  className="w-44 min-w-0"
                  aria-label="Search containers"
                />
                <Button type="submit" variant="outline" size="sm">Search</Button>
                {search !== '' && <Button variant="ghost" type="button" size="sm" onClick={() => {
                  setQuery('');
                  setSearch('');
                  setPage(0);
                }}>Clear</Button>}
              </form>
            </CardHeader>
            <CardContent>
              {containersError !== null && containers === null && (
                <ErrorState
                  title="Couldn't load containers"
                  message={containersError}
                  onRetry={() => setContainersAttempt((attempt) => attempt + 1)}
                />
              )}
              {containersError !== null && containers !== null && (
                <div className="mb-4 flex items-center gap-2 text-sm">
                  <span className="text-muted-foreground">
                    Refresh failed: {containersError} — showing last good inventory.
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => setContainersAttempt((attempt) => attempt + 1)}
                  >
                    Retry
                  </Button>
                </div>
              )}
              {freshness !== null && (
                <div className="mb-4 rounded-md border p-3">
                  <HeartbeatStatus
                    label="Docker inventory heartbeat"
                    {...inventoryHeartbeat(freshness)}
                    observedAt={freshness.reconciled_at}
                    detail={freshness.last_error_category === 'docker_inventory_unavailable'
                      ? 'Docker inventory is unreachable. Check the daemon and socket permissions. Retained records can still be listed.'
                      : freshness.last_error_category === 'docker_inspect_unavailable'
                        ? 'Some containers could not be inspected. Check Docker access for those containers.'
                        : freshness.stale ? 'The last reconciliation has aged out. Check the collector if the next sample is also late.'
                          : freshness.reconciled_at === null ? 'Waiting for Docker inventory to finish its first pass.' : undefined}
                    action={(freshness.stale || !freshness.complete) ? (
                      <Button variant="outline" size="sm" onClick={() => setContainersAttempt((attempt) => attempt + 1)}>
                        Refresh inventory
                      </Button>
                    ) : null}
                  />
                </div>
              )}
              <div className="mb-4 flex flex-wrap gap-2" aria-label="Container filters">
                {containerFilters.map((value) => (
                  <Button
                    key={value}
                    size="sm"
                    variant={filter === value ? 'default' : 'outline'}
                    aria-pressed={filter === value}
                    onClick={() => { setFilter(value); setPage(0); }}
                  >
                    {value[0]?.toUpperCase()}{value.slice(1)}
                  </Button>
                ))}
                <Select value={sort} onValueChange={(value) => { setSort(value); setPage(0); }}>
                  <SelectTrigger className="sm:ml-auto" aria-label="Sort containers"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="cpu">Highest CPU</SelectItem>
                    <SelectItem value="project">Group by Compose project</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {containers === null && containersError === null ? (
                <div className="space-y-2" aria-label="Loading containers">
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : containers === null ? null : containers.length === 0 ? (
                <p className="text-muted-foreground py-6 text-center text-sm">
                  {page > 0 ? 'This page is empty. Go back to the previous page.'
                    : filter !== 'all' || search !== '' ? 'No containers match these filters.'
                    : freshness?.reconciled_at ? 'No containers reported by the daemon.'
                    : 'Waiting for the first container inventory.'}
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead className="hidden md:table-cell">Project</TableHead>
                      <TableHead className="hidden md:table-cell">Management</TableHead>
                      <TableHead className="hidden md:table-cell">Runtime</TableHead>
                      <TableHead className="text-right">CPU</TableHead>
                      <TableHead className="hidden text-right md:table-cell">Memory</TableHead>
                      <TableHead className="hidden text-right md:table-cell">Restarts</TableHead>
                      <TableHead className="hidden text-right md:table-cell">Sampled</TableHead>
                      <TableHead className="text-right">Action</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {containers.map((container, index) => {
                      const mem = memPercent(container);
                      const serviceId = container.service_id ?? '';
                      const projectStart = sort === 'project' &&
                        (index === 0 || containers[index - 1]?.project !== container.project);

                      return (
                        <Fragment key={container.id}>
                          {projectStart && (
                            <TableRow className="bg-muted/40">
                              <TableCell colSpan={9} className="break-words whitespace-normal text-sm font-medium">
                                {container.project || 'No Compose project'}
                              </TableCell>
                            </TableRow>
                          )}
                          <TableRow>
                            <TableCell className="font-mono text-xs font-medium">
                              <button type="button" className="max-w-40 break-all whitespace-normal text-left hover:underline md:max-w-none md:break-normal md:whitespace-nowrap" onClick={() => onSelectContainer(container.id)}>
                                {container.name}
                              </button>
                              <div className="text-muted-foreground mt-1 space-y-1 font-sans font-normal whitespace-normal md:hidden">
                                <p className="break-words">{[container.project, container.state].filter(Boolean).join(' · ')}</p>
                                <ManagedBadge container={container} onSelectService={onSelectService} />
                                {container.mem_bytes !== null && <p>Memory {formatBytes(container.mem_bytes)}</p>}
                              </div>
                            </TableCell>
                            <TableCell className="hidden md:table-cell">
                              {container.project === '' ? (
                                <span className="text-muted-foreground">—</span>
                              ) : (
                                <Badge variant="outline">{container.project}</Badge>
                              )}
                            </TableCell>
                            <TableCell className="hidden md:table-cell">
                              <ManagedBadge container={container} onSelectService={onSelectService} />
                            </TableCell>
                            <TableCell className="hidden md:table-cell">
                              <div className="space-y-1">
                                {container.state === 'running' ? (
                                  <Badge variant="default">{container.state}</Badge>
                                ) : (
                                  <Badge variant="secondary">{container.state}</Badge>
                                )}
                                {container.status !== undefined && container.status !== '' && (
                                  <p className="text-muted-foreground max-w-56 text-xs">
                                    {container.status}
                                  </p>
                                )}
                              </div>
                            </TableCell>
                            <TableCell className="text-right font-mono">
                              {container.cpu_percent === null ? 'n/a' : `${container.cpu_percent.toFixed(1)}%`}
                            </TableCell>
                            <TableCell className="hidden text-right font-mono md:table-cell">
                              {container.mem_bytes === null ? 'n/a' : formatBytes(container.mem_bytes)}
                              {mem !== null && (
                                <span className="text-muted-foreground"> ({mem.toFixed(1)}%)</span>
                              )}
                            </TableCell>
                            <TableCell className="hidden text-right font-mono md:table-cell">
                              {container.restarts === null ? 'n/a' : container.restarts > 0 ? (
                                <Badge variant="destructive">{container.restarts}</Badge>
                              ) : (
                                container.restarts
                              )}
                            </TableCell>
                            <TableCell className="text-muted-foreground hidden text-right text-xs md:table-cell">
                              {container.sampled_at === undefined && container.last_seen_at === undefined
                                ? 'n/a'
                                : timeAgo(container.sampled_at ?? container.last_seen_at ?? '')}
                            </TableCell>
                            <TableCell className="text-right">
                              {!container.managed ? (
                                isAdmin ? (
                                  <Button size="sm" onClick={(event) => {
                                    promoteTrigger.current = event.currentTarget;
                                    setPromoteTarget(container.name);
                                  }}>
                                    Manage
                                  </Button>
                                ) : null
                              ) : (
                                serviceId !== '' && (
                                  <Button
                                    variant="ghost"
                                    size="sm"
                                    onClick={() => onSelectService(serviceId)}
                                  >
                                    Open
                                  </Button>
                                )
                              )}
                            </TableCell>
                          </TableRow>
                        </Fragment>
                      );
                    })}
                  </TableBody>
                </Table>
              )}
              {(page > 0 || containerPage?.scope === pageScope && containerPage.hasNext) && (
                <div className="mt-4 flex flex-wrap items-center justify-end gap-2" aria-label="Container pages">
                  <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage((value) => value - 1)}>
                    Previous
                  </Button>
                  <span className="text-muted-foreground text-sm">Page {page + 1}</span>
                  <Button variant="outline" size="sm" disabled={containerPage?.scope !== pageScope || !containerPage.hasNext}
                    onClick={() => setPage((value) => value + 1)}>
                    Next
                  </Button>
                  {page > 0 && (
                    <Button variant="ghost" size="sm" onClick={() => setContainersAttempt((value) => value + 1)}>
                      Refresh page
                    </Button>
                  )}
                </div>
              )}
            </CardContent>
      </Card>

      <details className="rounded-lg border" onToggle={(event) => setHistoryOpen(event.currentTarget.open)}>
        <summary className="cursor-pointer p-4 text-sm font-medium">Host history</summary>
        {historyOpen && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-end">
              <Select value={String(range)} onValueChange={(value) => setRange(Number(value))}>
                <SelectTrigger className="w-28" aria-label="History range">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {rangeOptions.map((option) => (
                    <SelectItem key={option.hours} value={String(option.hours)}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </CardHeader>
            <CardContent>
              {historyError !== null && historyLoading ? (
                <ErrorState
                  title="Couldn't load host history"
                  message={historyError}
                  onRetry={() => setHistoryAttempt((attempt) => attempt + 1)}
                />
              ) : historyLoading ? (
                <div className="grid gap-4 lg:grid-cols-3">
                  <Skeleton className="h-44 w-full" />
                  <Skeleton className="h-44 w-full" />
                  <Skeleton className="h-44 w-full" />
                </div>
              ) : (
                <>
                  {historyError !== null && (
                    <div className="mb-2 flex items-center gap-2 text-sm">
                      <span className="text-muted-foreground">
                        Refresh failed: {historyError} — showing last good data.
                      </span>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setHistoryAttempt((attempt) => attempt + 1)}
                      >
                        Retry
                      </Button>
                    </div>
                  )}
                  <ScreenErrorBoundary title="Couldn’t load host history charts">
                    <Suspense fallback={<Skeleton className="h-44 w-full" aria-label="Loading history charts" />}>
                      <FleetHistoryCharts cpu={cpu ?? []} mem={mem ?? []} load={loadAvg ?? []} range={range} />
                    </Suspense>
                  </ScreenErrorBoundary>
                </>
              )}
            </CardContent>
          </Card>
        )}
      </details>

      {system !== null && (
        <div className="grid gap-4 lg:grid-cols-3">
            <Card>
              <CardHeader>
                <CardTitle className="text-sm font-medium">Host</CardTitle>
              </CardHeader>
              <CardContent className="divide-y divide-border">
                <Row label="Hostname" value={system.hostname === '' ? 'n/a' : system.hostname} />
                <Row label="OS / arch" value={`${system.os} / ${system.arch}`} />
                <Row
                  label="Uptime"
                  value={system.uptime_secs === null ? 'n/a' : formatDuration(system.uptime_secs)}
                />
                <Row
                  label="Load (1m)"
                  value={system.load_1 === null ? 'n/a' : system.load_1.toFixed(2)}
                />
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-sm font-medium">Docker daemon</CardTitle>
              </CardHeader>
              <CardContent className="divide-y divide-border">
                {system.docker === null ? (
                  <p className="text-muted-foreground py-2 text-sm">Daemon unreachable.</p>
                ) : (
                  <>
                    <Row label="Server" value={system.docker.server_version} />
                    <Row label="OS" value={`${system.docker.operating_system} (${system.docker.architecture})`} />
                    <Row label="Kernel" value={system.docker.kernel_version} />
                    <Row label="CPUs" value={String(system.docker.ncpu)} />
                    <Row label="Memory" value={formatBytes(system.docker.mem_total_bytes)} />
                    <Row
                      label="Containers"
                      value={`${system.docker.containers_running} running / ${system.docker.containers_stopped} stopped`}
                    />
                    <Row label="Images" value={String(system.docker.images)} />
                  </>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-sm font-medium">touchgrass itself</CardTitle>
              </CardHeader>
              <CardContent className="divide-y divide-border">
                <Row label="Version" value={system.self.version} />
                <Row label="Uptime" value={formatDuration(system.self.uptime_secs)} />
                <Row label="Database" value={formatBytes(system.self.database_bytes)} />
              </CardContent>
            </Card>
        </div>
      )}

      <PromoteDialog
        container={promoteTarget}
        onClose={() => setPromoteTarget(null)}
        onRestoreFocus={() => {
          if (!promoteTrigger.current?.isConnected) return false;

          promoteTrigger.current.focus();
          return true;
        }}
        onCreated={(id) => {
          setPromoteTarget(null);
          onSelectService(id);
          setContainersAttempt((attempt) => attempt + 1);
        }}
        onUnauthorized={onUnauthorized}
      />
    </div>
  );
}
