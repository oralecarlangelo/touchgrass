import { useCallback, useEffect, useMemo, useState } from 'react';
import { Bar, BarChart, CartesianGrid, XAxis } from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from './ErrorState.tsx';
import {
  fetchNotifications,
  fetchSystem,
  isUnauthorized,
  type Notification,
  type ServiceView,
  type SystemSnapshot,
} from '@/lib/api.ts';
import { formatBytes, formatDuration, timeAgo } from '@/lib/format.ts';
import { healthDotClass, type ViewId } from '@/lib/views.ts';

const refreshIntervalMs = 30_000;

const activityConfig = {
  count: { label: 'Notifications', color: 'var(--chart-1)' },
} satisfies ChartConfig;

function bucketByHour(items: Notification[]): { hour: string; count: number }[] {
  const buckets = new Map<string, number>();
  const now = Date.now();

  for (let i = 23; i >= 0; i -= 1) {
    const date = new Date(now - i * 3600_000);
    buckets.set(`${date.getHours()}:00`, 0);
  }

  for (const item of items) {
    const at = new Date(item.created_at).getTime();

    if (Number.isNaN(at) || now - at > 24 * 3600_000) {
      continue;
    }

    const date = new Date(at);
    const key = `${date.getHours()}:00`;
    buckets.set(key, (buckets.get(key) ?? 0) + 1);
  }

  return [...buckets.entries()].map(([hour, count]) => ({ hour, count }));
}

export default function Dashboard({
  services,
  onSelectService,
  onView,
  onUnauthorized,
}: {
  services: ServiceView[] | null;
  onSelectService: (id: string) => void;
  onView: (view: ViewId) => void;
  onUnauthorized: () => void;
}) {
  const [system, setSystem] = useState<SystemSnapshot | null>(null);
  const [notes, setNotes] = useState<Notification[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      const [snapshot, fetched] = await Promise.all([
        fetchSystem(),
        fetchNotifications('', 100),
      ]);
      setSystem(snapshot);
      setNotes(fetched.notifications);
      setUnread(fetched.unread);
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

  const activity = useMemo(() => bucketByHour(notes ?? []), [notes]);
  const healthy = (services ?? []).filter((service) => service.health === 'healthy').length;

  if (error !== null && system === null) {
    return <ErrorState title="Couldn't load the dashboard" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Fleet health</CardTitle>
          </CardHeader>
          <CardContent>
            {services === null ? (
              <Skeleton className="h-8 w-20" />
            ) : (
              <p className="text-2xl font-semibold">
                {healthy}
                <span className="text-muted-foreground text-base font-normal">
                  /{services.length} healthy
                </span>
              </p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Unread notifications</CardTitle>
          </CardHeader>
          <CardContent>
            {notes === null ? (
              <Skeleton className="h-8 w-16" />
            ) : (
              <p className="text-2xl font-semibold">{unread}</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Host uptime</CardTitle>
          </CardHeader>
          <CardContent>
            {system === null ? (
              <Skeleton className="h-8 w-24" />
            ) : (
              <p className="text-2xl font-semibold">
                {system.uptime_secs === null ? 'n/a' : formatDuration(system.uptime_secs)}
              </p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Database</CardTitle>
          </CardHeader>
          <CardContent>
            {system === null ? (
              <Skeleton className="h-8 w-24" />
            ) : (
              <p className="text-2xl font-semibold">{formatBytes(system.self.database_bytes)}</p>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between pb-2">
          <CardTitle className="text-sm font-medium">Host resources (live)</CardTitle>
          <Button variant="ghost" size="sm" onClick={() => onView('system')}>
            System
          </Button>
        </CardHeader>
        <CardContent>
          {system === null ? (
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : (
            <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <div>
                <dt className="text-muted-foreground text-xs">CPU</dt>
                <dd className="font-mono text-lg font-semibold">
                  {system.cpu_percent === null ? 'n/a' : `${system.cpu_percent.toFixed(1)}%`}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground text-xs">Memory</dt>
                <dd className="font-mono text-lg font-semibold">
                  {system.mem_used_bytes === null || system.mem_total_bytes === null
                    ? 'n/a'
                    : `${formatBytes(system.mem_used_bytes)} / ${formatBytes(system.mem_total_bytes)}`}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground text-xs">Disk</dt>
                <dd className="font-mono text-lg font-semibold">
                  {system.disk_used_bytes === null || system.disk_total_bytes === null
                    ? 'n/a'
                    : `${formatBytes(system.disk_used_bytes)} / ${formatBytes(system.disk_total_bytes)}`}
                </dd>
              </div>
              <div>
                <dt className="text-muted-foreground text-xs">Load (1m)</dt>
                <dd className="font-mono text-lg font-semibold">
                  {system.load_1 === null ? 'n/a' : system.load_1.toFixed(2)}
                </dd>
              </div>
            </dl>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle className="text-sm font-medium">Services</CardTitle>
            <Button variant="ghost" size="sm" onClick={() => onView('services')}>
              View all
            </Button>
          </CardHeader>
          <CardContent className="space-y-1">
            {services === null && (
              <>
                <Skeleton className="h-10 w-full" />
                <Skeleton className="h-10 w-full" />
              </>
            )}
            {(services ?? []).map((service) => (
              <button
                key={service.id}
                type="button"
                onClick={() => onSelectService(service.id)}
                className="hover:bg-muted/50 flex w-full items-center gap-3 rounded-md px-2 py-2 text-left"
              >
                <span
                  className={`h-2 w-2 shrink-0 rounded-full ${healthDotClass(service.health)}`}
                  aria-hidden
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{service.name}</span>
                  <span className="text-muted-foreground block text-xs">
                    {service.strategy}
                    {service.live_color !== '' && ` · live ${service.live_color}`}
                  </span>
                </span>
                <Badge
                  variant={
                    service.health === 'healthy'
                      ? 'default'
                      : service.health === 'unknown'
                        ? 'secondary'
                        : 'destructive'
                  }
                >
                  {service.health}
                </Badge>
              </button>
            ))}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle className="text-sm font-medium">Notifications, last 24h</CardTitle>
            <Button variant="ghost" size="sm" onClick={() => onView('notifications')}>
              View all
            </Button>
          </CardHeader>
          <CardContent>
            {notes === null ? (
              <Skeleton className="h-40 w-full" />
            ) : (
              <ChartContainer config={activityConfig} className="h-40 w-full">
                <BarChart data={activity}>
                  <CartesianGrid vertical={false} />
                  <XAxis dataKey="hour" tickLine={false} axisLine={false} interval={5} />
                  <ChartTooltip content={<ChartTooltipContent />} />
                  <Bar dataKey="count" fill="var(--color-count)" radius={2} />
                </BarChart>
              </ChartContainer>
            )}
            <ul className="mt-2 space-y-1">
              {(notes ?? []).slice(0, 3).map((item) => (
                <li key={item.id} className="flex items-center gap-2 text-sm">
                  {!item.read_at && (
                    <span className="bg-primary h-1.5 w-1.5 shrink-0 rounded-full" aria-hidden />
                  )}
                  <span className="min-w-0 flex-1 truncate">{item.title}</span>
                  <span className="text-muted-foreground shrink-0 text-xs">
                    {timeAgo(item.created_at)}
                  </span>
                </li>
              ))}
              {notes !== null && notes.length === 0 && (
                <li className="text-muted-foreground text-sm">Quiet. Prod is covered.</li>
              )}
            </ul>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
