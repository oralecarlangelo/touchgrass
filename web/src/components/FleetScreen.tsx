import { useEffect, useMemo, useState } from 'react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
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
import PromoteDialog from './PromoteDialog.tsx';
import {
  fetchFleetContainers,
  fetchSystem,
  fetchSystemHistory,
  isUnauthorized,
  type ContainerFreshness,
  type FleetContainer,
  type HistoryPoint,
  type SystemSnapshot,
} from '@/lib/api.ts';
import { formatBytes, formatDuration, timeAgo } from '@/lib/format.ts';

const refreshIntervalMs = 30_000;

const rangeOptions = [
  { hours: 1, label: '1h' },
  { hours: 6, label: '6h' },
  { hours: 24, label: '24h' },
  { hours: 168, label: '7d' },
];

const cpuConfig = {
  cpu: { label: 'CPU %', color: 'var(--chart-1)' },
} satisfies ChartConfig;

const memConfig = {
  mem: { label: 'Memory', color: 'var(--chart-2)' },
} satisfies ChartConfig;

const loadConfig = {
  load: { label: 'Load (1m)', color: 'var(--chart-3)' },
} satisfies ChartConfig;

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

function formatTick(ts: string, hours: number): string {
  const parsed = new Date(ts);

  if (Number.isNaN(parsed.getTime())) {
    return ts;
  }

  const time = parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

  if (hours <= 24) {
    return time;
  }

  return `${parsed.getMonth() + 1}/${parsed.getDate()} ${time}`;
}

function toSeries(points: HistoryPoint[], hours: number): { at: string; value: number }[] {
  return [...points]
    .sort((a, b) => (a.ts < b.ts ? -1 : a.ts > b.ts ? 1 : 0))
    .map((point) => ({ at: formatTick(point.ts, hours), value: point.value }));
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

export type ContainerFilter = 'all' | 'running' | 'stopped' | 'unknown' | 'risky';

export function filterFleetContainers(
  containers: FleetContainer[],
  filter: ContainerFilter,
  query: string,
): FleetContainer[] {
  const needle = query.trim().toLowerCase();

  return containers
    .filter((container) => {
      const matchesText = needle === '' || container.name.toLowerCase().includes(needle) ||
        container.project.toLowerCase().includes(needle) || container.id.toLowerCase().includes(needle);
      if (!matchesText) return false;
      switch (filter) {
        case 'running': return container.state === 'running';
        case 'stopped': return container.state !== 'running';
        case 'unknown': return container.classification === 'unknown';
        case 'risky': return Object.values(container.risk_counts).some((count) => count > 0);
        case 'all': return true;
      }
    })
    .sort((a, b) => {
      if (a.managed !== b.managed) return a.managed ? 1 : -1;
      return (b.cpu_percent ?? -1) - (a.cpu_percent ?? -1) || a.name.localeCompare(b.name);
    });
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
  const [containers, setContainers] = useState<FleetContainer[] | null>(null);
  const [freshness, setFreshness] = useState<ContainerFreshness | null>(null);
  const [systemError, setSystemError] = useState<string | null>(null);
  const [containersError, setContainersError] = useState<string | null>(null);
  const [range, setRange] = useState(24);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [cpu, setCpu] = useState<HistoryPoint[] | null>(null);
  const [mem, setMem] = useState<HistoryPoint[] | null>(null);
  const [loadAvg, setLoadAvg] = useState<HistoryPoint[] | null>(null);
  const [loadedRange, setLoadedRange] = useState<number | null>(null);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [promoteTarget, setPromoteTarget] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<ContainerFilter>('all');
  const [systemAttempt, setSystemAttempt] = useState(0);
  const [containersAttempt, setContainersAttempt] = useState(0);
  const [historyAttempt, setHistoryAttempt] = useState(0);

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
        const fleet = await fetchFleetContainers(controller.signal);
        if (!controller.signal.aborted) {
          setContainers(fleet.containers);
          setFreshness(fleet.freshness);
          setContainersError(null);
        }
      } catch (err) {
        if (controller.signal.aborted) return;
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }
        setContainersError(err instanceof Error ? err.message : String(err));
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
  }, [containersAttempt, onUnauthorized]);

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
        const [cpuPoints, memPoints, loadPoints] = await Promise.all([
          fetchSystemHistory('cpu', range, controller.signal),
          fetchSystemHistory('mem', range, controller.signal),
          fetchSystemHistory('load', range, controller.signal),
        ]);
        if (!controller.signal.aborted) {
          setCpu(cpuPoints);
          setMem(memPoints);
          setLoadAvg(loadPoints);
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

  const cpuSeries = useMemo(() => toSeries(cpu ?? [], range), [cpu, range]);
  const memSeries = useMemo(() => toSeries(mem ?? [], range), [mem, range]);
  const loadSeries = useMemo(() => toSeries(loadAvg ?? [], range), [loadAvg, range]);
  const historyLoading = loadedRange !== range || cpu === null || mem === null || loadAvg === null;

  const managedCount = useMemo(
    () => (containers ?? []).filter((container) => container.managed).length,
    [containers],
  );
  const unmanagedCount = (containers?.length ?? 0) - managedCount;

  const visibleContainers = useMemo(() => {
    return filterFleetContainers(containers ?? [], filter, query);
  }, [containers, filter, query]);

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold tracking-tight">Fleet</h1>

      {freshness?.stale === true && (
        <div className="border-amber-500/40 bg-amber-500/10 rounded-md border px-4 py-3 text-sm" role="status">
          Container collection is stale. Inventory may be incomplete until the Docker collector recovers.
        </div>
      )}

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

      {containers !== null && (
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Top container CPU</CardTitle>
            <CardDescription>Latest available samples. 100% represents one CPU core.</CardDescription>
          </CardHeader>
          <CardContent>
            <ul className="divide-y">
              {containers.filter((container) => (container.cpu_percent ?? 0) > 0)
                .sort((a, b) => (b.cpu_percent ?? 0) - (a.cpu_percent ?? 0))
                .slice(0, 5)
                .map((container) => (
                  <li key={container.id} className="flex justify-between gap-3 py-1.5 text-sm">
                    <button type="button" className="min-w-0 truncate text-left underline-offset-4 hover:underline"
                      onClick={() => onSelectContainer(container.id)}>{container.name}</button>
                    <span className="shrink-0 font-mono">{container.cpu_percent?.toFixed(1)}%</span>
                  </li>
                ))}
            </ul>
            {!containers.some((container) => (container.cpu_percent ?? 0) > 0) && (
              <p className="text-muted-foreground text-sm">No container CPU usage sampled.</p>
            )}
          </CardContent>
        </Card>
      )}

          <Card>
            <CardHeader className="flex flex-row items-start justify-between gap-4">
              <div className="space-y-1.5">
                <CardTitle className="text-sm font-medium">
                  Containers{containers === null ? '' : ` (${containers.length})`}
                </CardTitle>
                {containers !== null && (
                  <CardDescription>
                    {managedCount} managed · {unmanagedCount} unmanaged
                    {unmanagedCount > 0
                      ? ' — press Manage on an unmanaged container to bring it under touchgrass deploys.'
                      : ' — everything on this host is under management.'}
                  </CardDescription>
                )}
              </div>
              <Input
                placeholder="Filter…"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                className="max-w-44"
                aria-label="Filter containers"
              />
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
              <div className="mb-4 flex flex-wrap gap-2" aria-label="Container filters">
                {(['all', 'running', 'stopped', 'unknown', 'risky'] as ContainerFilter[]).map((value) => (
                  <Button
                    key={value}
                    size="sm"
                    variant={filter === value ? 'default' : 'outline'}
                    onClick={() => setFilter(value)}
                  >
                    {value[0]?.toUpperCase()}{value.slice(1)}
                  </Button>
                ))}
              </div>
              {containers === null && containersError === null ? (
                <div className="space-y-2" aria-label="Loading containers">
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : containers === null ? null : containers.length === 0 ? (
                <p className="text-muted-foreground py-6 text-center text-sm">
                  No containers reported by the daemon.
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Project</TableHead>
                      <TableHead>Management</TableHead>
                      <TableHead>Runtime</TableHead>
                      <TableHead className="text-right">CPU</TableHead>
                      <TableHead className="text-right">Memory</TableHead>
                      <TableHead className="text-right">Restarts</TableHead>
                      <TableHead className="text-right">Sampled</TableHead>
                      <TableHead className="text-right">Action</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visibleContainers.map((container) => {
                      const mem = memPercent(container);
                      const serviceId = container.service_id ?? '';

                      return (
                        <TableRow key={container.id}>
                          <TableCell className="font-mono text-xs font-medium">
                            <button type="button" className="hover:underline" onClick={() => onSelectContainer(container.id)}>
                              {container.name}
                            </button>
                          </TableCell>
                          <TableCell>
                            {container.project === '' ? (
                              <span className="text-muted-foreground">—</span>
                            ) : (
                              <Badge variant="outline">{container.project}</Badge>
                            )}
                          </TableCell>
                          <TableCell>
                            <ManagedBadge container={container} onSelectService={onSelectService} />
                          </TableCell>
                          <TableCell>
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
                          <TableCell className="text-right font-mono">
                            {container.mem_bytes === null ? 'n/a' : formatBytes(container.mem_bytes)}
                            {mem !== null && (
                              <span className="text-muted-foreground"> ({mem.toFixed(1)}%)</span>
                            )}
                          </TableCell>
                          <TableCell className="text-right font-mono">
                            {container.restarts === null ? 'n/a' : container.restarts > 0 ? (
                              <Badge variant="destructive">{container.restarts}</Badge>
                            ) : (
                              container.restarts
                            )}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-right text-xs">
                            {container.sampled_at === undefined && container.last_seen_at === undefined
                              ? 'n/a'
                              : timeAgo(container.sampled_at ?? container.last_seen_at ?? '')}
                          </TableCell>
                          <TableCell className="text-right">
                            {!container.managed ? (
                              isAdmin ? (
                                <Button size="sm" onClick={() => setPromoteTarget(container.name)}>
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
                      );
                    })}
                    {visibleContainers.length === 0 && (
                      <TableRow>
                        <TableCell colSpan={9} className="text-muted-foreground text-center">
                          No containers match this filter.
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
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
                  <div className="grid gap-4 lg:grid-cols-3">
                    <div>
                      <p className="text-muted-foreground mb-1 text-xs">CPU %</p>
                      {cpuSeries.length === 0 ? (
                        <p className="text-muted-foreground py-6 text-center text-sm">
                          No CPU samples in range.
                        </p>
                      ) : (
                        <ChartContainer config={cpuConfig} className="h-44 w-full">
                          <LineChart data={cpuSeries}>
                            <CartesianGrid vertical={false} />
                            <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
                            <YAxis tickLine={false} axisLine={false} width={40} />
                            <ChartTooltip content={<ChartTooltipContent />} />
                            <Line type="monotone" dataKey="value" stroke="var(--color-cpu)" dot={false} />
                          </LineChart>
                        </ChartContainer>
                      )}
                    </div>
                    <div>
                      <p className="text-muted-foreground mb-1 text-xs">Memory used</p>
                      {memSeries.length === 0 ? (
                        <p className="text-muted-foreground py-6 text-center text-sm">
                          No memory samples in range.
                        </p>
                      ) : (
                        <ChartContainer config={memConfig} className="h-44 w-full">
                          <LineChart data={memSeries}>
                            <CartesianGrid vertical={false} />
                            <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
                            <YAxis
                              tickLine={false}
                              axisLine={false}
                              width={52}
                              tickFormatter={(value: number) => formatBytes(value)}
                            />
                            <ChartTooltip content={<ChartTooltipContent />} />
                            <Line type="monotone" dataKey="value" stroke="var(--color-mem)" dot={false} />
                          </LineChart>
                        </ChartContainer>
                      )}
                    </div>
                    <div>
                      <p className="text-muted-foreground mb-1 text-xs">Load (1m)</p>
                      {loadSeries.length === 0 ? (
                        <p className="text-muted-foreground py-6 text-center text-sm">
                          No load samples in range.
                        </p>
                      ) : (
                        <ChartContainer config={loadConfig} className="h-44 w-full">
                          <LineChart data={loadSeries}>
                            <CartesianGrid vertical={false} />
                            <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
                            <YAxis tickLine={false} axisLine={false} width={36} />
                            <ChartTooltip content={<ChartTooltipContent />} />
                            <Line type="monotone" dataKey="value" stroke="var(--color-load)" dot={false} />
                          </LineChart>
                        </ChartContainer>
                      )}
                    </div>
                  </div>
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
