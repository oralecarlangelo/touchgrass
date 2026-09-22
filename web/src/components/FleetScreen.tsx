import { useCallback, useEffect, useMemo, useState } from 'react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
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
import ErrorState from './ErrorState.tsx';
import {
  fetchFleetContainers,
  fetchSystem,
  fetchSystemHistory,
  isUnauthorized,
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
  if (container.mem_limit <= 0) {
    return null;
  }

  return (container.mem_bytes / container.mem_limit) * 100;
}

export default function FleetScreen({
  onUnauthorized,
  onSelectService,
}: {
  onUnauthorized: () => void;
  onSelectService: (id: string) => void;
}) {
  const [system, setSystem] = useState<SystemSnapshot | null>(null);
  const [containers, setContainers] = useState<FleetContainer[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [range, setRange] = useState(24);
  const [cpu, setCpu] = useState<HistoryPoint[] | null>(null);
  const [mem, setMem] = useState<HistoryPoint[] | null>(null);
  const [loadAvg, setLoadAvg] = useState<HistoryPoint[] | null>(null);
  const [historyError, setHistoryError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      const [snapshot, fleet] = await Promise.all([fetchSystem(), fetchFleetContainers()]);
      setSystem(snapshot);
      setContainers(fleet);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  const loadHistory = useCallback(
    async (hours: number) => {
      setHistoryError(null);

      try {
        const [cpuPoints, memPoints, loadPoints] = await Promise.all([
          fetchSystemHistory('cpu', hours),
          fetchSystemHistory('mem', hours),
          fetchSystemHistory('load', hours),
        ]);
        setCpu(cpuPoints);
        setMem(memPoints);
        setLoadAvg(loadPoints);
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setHistoryError(err instanceof Error ? err.message : String(err));
      }
    },
    [onUnauthorized],
  );

  useEffect(() => {
    void load();
    void loadHistory(range);
    const timer = setInterval(() => {
      void load();
      void loadHistory(range);
    }, refreshIntervalMs);

    return () => clearInterval(timer);
  }, [load, loadHistory, range]);

  const cpuSeries = useMemo(() => toSeries(cpu ?? [], range), [cpu, range]);
  const memSeries = useMemo(() => toSeries(mem ?? [], range), [mem, range]);
  const loadSeries = useMemo(() => toSeries(loadAvg ?? [], range), [loadAvg, range]);
  const historyLoading = cpu === null || mem === null || loadAvg === null;

  if (error !== null && system === null) {
    return <ErrorState title="Couldn't load fleet info" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold tracking-tight">Fleet</h1>

      {system === null ? (
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

          <Card>
            <CardHeader className="flex flex-row items-center justify-between">
              <CardTitle className="text-sm font-medium">Host history</CardTitle>
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
                  onRetry={() => void loadHistory(range)}
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
                        onClick={() => void loadHistory(range)}
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

          <Card>
            <CardHeader>
              <CardTitle className="text-sm font-medium">
                Containers{containers === null ? '' : ` (${containers.length})`}
              </CardTitle>
            </CardHeader>
            <CardContent>
              {containers === null ? (
                <div className="space-y-2" aria-label="Loading containers">
                  <Skeleton className="h-10 w-full" />
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : containers.length === 0 ? (
                <p className="text-muted-foreground py-6 text-center text-sm">
                  No containers reported by the daemon.
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Project</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead className="text-right">CPU</TableHead>
                      <TableHead className="text-right">Memory</TableHead>
                      <TableHead className="text-right">Restarts</TableHead>
                      <TableHead className="text-right">Sampled</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {containers.map((container) => {
                      const mem = memPercent(container);

                      return (
                        <TableRow key={container.name}>
                          <TableCell className="font-mono text-xs font-medium">
                            {container.name}
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
                            {container.state === 'running' ? (
                              <Badge variant="default">{container.state}</Badge>
                            ) : (
                              <Badge variant="secondary">{container.state}</Badge>
                            )}
                          </TableCell>
                          <TableCell className="text-right font-mono">
                            {container.cpu_percent.toFixed(1)}%
                          </TableCell>
                          <TableCell className="text-right font-mono">
                            {formatBytes(container.mem_bytes)}
                            {mem !== null && (
                              <span className="text-muted-foreground"> ({mem.toFixed(1)}%)</span>
                            )}
                          </TableCell>
                          <TableCell className="text-right font-mono">
                            {container.restarts > 0 ? (
                              <Badge variant="destructive">{container.restarts}</Badge>
                            ) : (
                              container.restarts
                            )}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-right text-xs">
                            {timeAgo(container.sampled_at)}
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

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
        </>
      )}
    </div>
  );
}
