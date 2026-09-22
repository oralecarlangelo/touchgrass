import { useCallback, useEffect, useMemo, useState } from 'react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';

import { Badge } from '@/components/ui/badge';
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
import ErrorState from '../ErrorState.tsx';
import { fetchMetrics, isUnauthorized, type Metric } from '@/lib/api.ts';
import { formatBytes, formatDuration } from '@/lib/format.ts';

const seriesConfig = {
  cpu: { label: 'CPU %', color: 'var(--chart-1)' },
  mem: { label: 'Mem %', color: 'var(--chart-2)' },
} satisfies ChartConfig;

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

export default function MetricsTab({
  serviceId,
  onUnauthorized,
}: {
  serviceId: string;
  onUnauthorized: () => void;
}) {
  const [metrics, setMetrics] = useState<Metric[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [container, setContainer] = useState<string>('all');

  const load = useCallback(async () => {
    setError(null);

    try {
      setMetrics(await fetchMetrics(serviceId));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [serviceId, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  const containers = useMemo(
    () => [...new Set((metrics ?? []).map((metric) => metric.container_name))].sort(),
    [metrics],
  );

  const series = useMemo(() => {
    const rows = (metrics ?? [])
      .filter((metric) => container === 'all' || metric.container_name === container)
      .sort((a, b) => (a.sampled_at < b.sampled_at ? -1 : 1));

    if (container !== 'all') {
      return rows.map((metric) => ({
        at: shortTime(metric.sampled_at),
        cpu: Number(metric.cpu_percent.toFixed(1)),
        mem: memPercent(metric) === null ? 0 : Number((memPercent(metric) ?? 0).toFixed(1)),
      }));
    }

    // "All" averages across containers per timestamp.
    const buckets = new Map<string, { cpu: number; mem: number; count: number }>();

    for (const metric of rows) {
      const bucket = buckets.get(metric.sampled_at) ?? { cpu: 0, mem: 0, count: 0 };
      bucket.cpu += metric.cpu_percent;
      bucket.mem += memPercent(metric) ?? 0;
      bucket.count += 1;
      buckets.set(metric.sampled_at, bucket);
    }

    return [...buckets.entries()]
      .sort(([a], [b]) => (a < b ? -1 : 1))
      .map(([at, bucket]) => ({
        at: shortTime(at),
        cpu: Number((bucket.cpu / bucket.count).toFixed(1)),
        mem: Number((bucket.mem / bucket.count).toFixed(1)),
      }));
  }, [metrics, container]);

  const latest = useMemo(() => latestByContainer(metrics ?? []), [metrics]);

  if (error !== null) {
    return <ErrorState title="Couldn't load metrics" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-sm font-medium">CPU / memory over time (%)</CardTitle>
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
            <p className="text-muted-foreground text-sm">
              No samples yet — the sampler records every 30 seconds.
            </p>
          ) : (
            <ChartContainer config={seriesConfig} className="h-56 w-full">
              <LineChart data={series}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
                <YAxis tickLine={false} axisLine={false} width={40} />
                <ChartTooltip content={<ChartTooltipContent />} />
                <Line type="monotone" dataKey="cpu" stroke="var(--color-cpu)" dot={false} />
                <Line type="monotone" dataKey="mem" stroke="var(--color-mem)" dot={false} />
              </LineChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">Latest sample per container</CardTitle>
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
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Container</TableHead>
                  <TableHead className="text-right">CPU</TableHead>
                  <TableHead className="text-right">Memory</TableHead>
                  <TableHead className="text-right">Disk</TableHead>
                  <TableHead className="text-right">Restarts</TableHead>
                  <TableHead className="text-right">Uptime</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {latest.map((sample) => {
                  const mem = memPercent(sample);

                  return (
                    <TableRow key={sample.container_name}>
                      <TableCell className="font-medium">{sample.container_name}</TableCell>
                      <TableCell className="text-right font-mono">
                        {sample.cpu_percent.toFixed(1)}%
                      </TableCell>
                      <TableCell className="text-right font-mono">
                        {formatBytes(sample.mem_bytes)}
                        {mem !== null && (
                          <span className="text-muted-foreground"> ({mem.toFixed(1)}%)</span>
                        )}
                      </TableCell>
                      <TableCell className="text-right font-mono">
                        {formatBytes(sample.disk_bytes)}
                      </TableCell>
                      <TableCell className="text-right font-mono">
                        {sample.restarts > 0 ? (
                          <Badge variant="destructive">{sample.restarts}</Badge>
                        ) : (
                          sample.restarts
                        )}
                      </TableCell>
                      <TableCell className="text-muted-foreground text-right font-mono">
                        {formatDuration(sample.uptime_secs)}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
