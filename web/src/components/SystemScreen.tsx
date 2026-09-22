import { useCallback, useEffect, useState } from 'react';
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from './ErrorState.tsx';
import { fetchSystem, isUnauthorized, type SystemSnapshot } from '@/lib/api.ts';
import { formatBytes, formatDuration } from '@/lib/format.ts';

const refreshIntervalMs = 30_000;
const sparklinePoints = 20;

const loadConfig = {
  load: { label: 'Load (1m)', color: 'var(--chart-1)' },
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

export default function SystemScreen({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [system, setSystem] = useState<SystemSnapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadHistory, setLoadHistory] = useState<{ at: string; load: number }[]>([]);

  const load = useCallback(async () => {
    setError(null);

    try {
      const snapshot = await fetchSystem();
      setSystem(snapshot);

      if (snapshot.load_1 !== null) {
        const at = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
        setLoadHistory((current) =>
          [...current, { at, load: snapshot.load_1 ?? 0 }].slice(-sparklinePoints),
        );
      }
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), refreshIntervalMs);

    return () => clearInterval(timer);
  }, [load]);

  if (error !== null && system === null) {
    return <ErrorState title="Couldn't load system info" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <h1 className="text-lg font-semibold tracking-tight">System</h1>

      {system === null ? (
        <div className="grid gap-4 lg:grid-cols-3">
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
          <Skeleton className="h-48 w-full" />
        </div>
      ) : (
        <>
          <div className="grid gap-4 lg:grid-cols-2">
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
              <CardHeader>
                <CardTitle className="text-sm font-medium">Load, this session</CardTitle>
              </CardHeader>
              <CardContent>
                {loadHistory.length < 2 ? (
                  <p className="text-muted-foreground py-6 text-center text-sm">
                    Collecting samples — one per refresh.
                  </p>
                ) : (
                  <ChartContainer config={loadConfig} className="h-40 w-full">
                    <LineChart data={loadHistory}>
                      <CartesianGrid vertical={false} />
                      <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
                      <YAxis tickLine={false} axisLine={false} width={36} />
                      <ChartTooltip content={<ChartTooltipContent />} />
                      <Line type="monotone" dataKey="load" stroke="var(--color-load)" dot={false} />
                    </LineChart>
                  </ChartContainer>
                )}
              </CardContent>
            </Card>
          </div>

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
