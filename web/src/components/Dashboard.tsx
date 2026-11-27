import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from './ErrorState.tsx';
import {
  fetchNotifications,
  fetchFleetContainers,
  fetchSystem,
  isUnauthorized,
  type Notification,
  type FleetContainersResponse,
  type ServiceView,
  type ServiceFreshness,
  type SystemSnapshot,
} from '@/lib/api.ts';
import { formatBytes, formatDuration, timeAgo } from '@/lib/format.ts';
import { healthDotClass, type ViewId } from '@/lib/views.ts';

const refreshIntervalMs = 30_000;

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export async function settleRegion<T>(
  request: Promise<T>,
  isCurrent: () => boolean,
  onSuccess: (value: T) => void,
  onError: (message: string) => void,
  onUnauthorized: () => void,
): Promise<void> {
  try {
    const value = await request;
    if (isCurrent()) onSuccess(value);
  } catch (error) {
    if (!isCurrent()) return;
    if (isUnauthorized(error)) {
      onUnauthorized();
      return;
    }
    onError(errorMessage(error));
  }
}

export function collectionFreshnessLabel(freshness: ServiceFreshness): string {
  const state = freshness.stale || !freshness.complete ? 'Degraded' : 'Current';
  return freshness.observed_at === null ? `${state} · never observed` : `${state} · observed ${timeAgo(freshness.observed_at)}`;
}

export function containerPostureLabel(fleet: FleetContainersResponse): string {
  if (fleet.freshness.stale || !fleet.freshness.complete) return 'degraded collection';
  const critical = fleet.containers.filter((container) => container.risk_counts.critical > 0).length;
  const unknown = fleet.containers.filter((container) => container.classification === 'unknown').length;
  const stopped = fleet.containers.filter((container) => container.state !== 'running').length;
  if (critical > 0) return `${critical} critical`;
  if (unknown > 0) return `${unknown} unknown`;
  if (stopped > 0) return `${stopped} stopped`;
  return `${fleet.containers.length} observed`;
}

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

function NotificationActivity({ activity }: { activity: ReturnType<typeof bucketByHour> }) {
  const peak = Math.max(1, ...activity.map(({ count }) => count));

  return (
    <figure className="space-y-1">
      <div className="border-border flex h-32 items-end gap-px border-b sm:gap-1" aria-hidden="true">
        {activity.map(({ hour, count }) => (
          <div key={hour} className="flex h-full min-w-0 flex-1 items-end" title={`${hour} · ${count} notifications`}>
            <span className="w-full rounded-t bg-chart-1" style={{ height: `${count / peak * 100}%` }} />
          </div>
        ))}
      </div>
      <div className="text-muted-foreground flex text-xs" aria-hidden="true">
        {activity.filter((_, index) => index % 6 === 0).map(({ hour }) => (
          <span key={hour} className="flex-1">{hour}</span>
        ))}
      </div>
      <figcaption className="sr-only">
        Notifications by hour. {activity.map(({ hour, count }) => `${hour}, ${count}`).join('; ')}.
      </figcaption>
    </figure>
  );
}

export default function Dashboard({
  services,
  servicesError,
  servicesRefreshing,
  servicesUpdatedAt,
  servicesFreshness,
  onRetryServices,
  onSelectService,
  onView,
  onUnauthorized,
  canHost,
}: {
  services: ServiceView[] | null;
  servicesError: string | null;
  servicesRefreshing: boolean;
  servicesUpdatedAt: string | null;
  servicesFreshness: ServiceFreshness | null;
  onRetryServices: () => void;
  onSelectService: (id: string) => void;
  onView: (view: ViewId) => void;
  onUnauthorized: () => void;
  // Whether the user may see the host: system snapshot and container
  // inventory. A user scoped to some services may not, so those calls
  // are never made and their cards are not drawn.
  canHost: boolean;
}) {
  const [system, setSystem] = useState<SystemSnapshot | null>(null);
  const [notes, setNotes] = useState<Notification[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [fleet, setFleet] = useState<FleetContainersResponse | null>(null);
  const [systemError, setSystemError] = useState<string | null>(null);
  const [notesError, setNotesError] = useState<string | null>(null);
  const [fleetError, setFleetError] = useState<string | null>(null);
  const requestRef = useRef<AbortController | null>(null);

  const load = useCallback(() => {
    requestRef.current?.abort();
    const controller = new AbortController();
    requestRef.current = controller;
    const isCurrent = () => requestRef.current === controller && !controller.signal.aborted;

    if (canHost) {
      void settleRegion(fetchSystem(controller.signal), isCurrent, (snapshot) => {
        setSystem(snapshot);
        setSystemError(null);
      }, setSystemError, onUnauthorized);
      void settleRegion(fetchFleetContainers(controller.signal), isCurrent, (snapshot) => {
        setFleet(snapshot);
        setFleetError(null);
      }, setFleetError, onUnauthorized);
    }

    void settleRegion(fetchNotifications('', 100, controller.signal), isCurrent, (fetched) => {
      setNotes(fetched.notifications);
      setUnread(fetched.unread);
      setNotesError(null);
    }, setNotesError, onUnauthorized);
  }, [onUnauthorized, canHost]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), refreshIntervalMs);

    return () => {
      clearInterval(timer);
      requestRef.current?.abort();
    };
  }, [load]);

  const activity = useMemo(() => bucketByHour(notes ?? []), [notes]);
  const healthy = (services ?? []).filter((service) => service.health === 'healthy').length;

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Managed services</CardTitle>
          </CardHeader>
          <CardContent>
            {services === null && servicesError !== null ? (
              <button className="text-destructive text-left text-sm" onClick={onRetryServices}>Services unavailable · Retry</button>
            ) : services === null ? (
              <Skeleton className="h-8 w-20" aria-label="Loading managed services" />
            ) : (
              <div>
                <p className="text-2xl font-semibold">
                  {healthy}<span className="text-muted-foreground text-base font-normal">/{services.length} healthy</span>
                </p>
                <p className="text-muted-foreground text-xs">
                  {servicesError !== null
                    ? 'Refresh failed · showing last known state'
                    : servicesFreshness?.stale || servicesFreshness?.complete === false
                      ? `Degraded · ${servicesUpdatedAt === null ? 'never observed' : `observed ${timeAgo(servicesUpdatedAt)}`}`
                      : servicesRefreshing
                        ? 'Refreshing…'
                        : servicesUpdatedAt === null ? 'Freshness unknown' : `Updated ${timeAgo(servicesUpdatedAt)}`}
                </p>
              </div>
            )}
          </CardContent>
        </Card>
        {canHost && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">Containers</CardTitle>
            </CardHeader>
            <CardContent>
              {fleet === null && fleetError !== null ? <button className="text-destructive text-sm" onClick={() => void load()}>Unavailable · Retry</button> : fleet === null ? <Skeleton className="h-8 w-24" /> : (
                <div>
                  <p className="text-2xl font-semibold">{fleet.containers.length}</p>
                  <p className="text-muted-foreground text-xs">{containerPostureLabel(fleet)}</p>
                </div>
              )}
            </CardContent>
          </Card>
        )}
        {canHost && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">Collector freshness</CardTitle>
            </CardHeader>
            <CardContent>
              {fleet === null && fleetError !== null ? <button className="text-destructive text-sm" onClick={() => void load()}>Unavailable · Retry</button> : fleet === null ? <Skeleton className="h-8 w-24" /> : (
                <div>
                  <p className="text-2xl font-semibold">
                    {fleet.freshness.stale || !fleet.freshness.complete ? 'Degraded' : 'Current'}
                  </p>
                  <p className="text-muted-foreground text-xs">
                    {fleet.freshness.reconciled_at === null ? 'Never reconciled' : timeAgo(fleet.freshness.reconciled_at)}
                  </p>
                </div>
              )}
            </CardContent>
          </Card>
        )}
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Unread notifications</CardTitle>
          </CardHeader>
          <CardContent>
            {notes === null && notesError !== null ? (
              <button className="text-destructive text-sm" onClick={() => void load()}>Unavailable · Retry</button>
            ) : notes === null ? (
              <Skeleton className="h-8 w-16" />
            ) : (
              <p className="text-2xl font-semibold">{unread}</p>
            )}
          </CardContent>
        </Card>
        {canHost && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">Host uptime</CardTitle>
            </CardHeader>
            <CardContent>
              {system === null && systemError !== null ? (
                <button className="text-destructive text-sm" onClick={() => void load()}>Unavailable · Retry</button>
              ) : system === null ? (
                <Skeleton className="h-8 w-24" />
              ) : (
                <p className="text-2xl font-semibold">
                  {system.uptime_secs === null ? 'n/a' : formatDuration(system.uptime_secs)}
                </p>
              )}
            </CardContent>
          </Card>
        )}
        {canHost && (
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-medium">Database</CardTitle>
            </CardHeader>
            <CardContent>
              {system === null && systemError !== null ? (
                <button className="text-destructive text-sm" onClick={() => void load()}>Unavailable · Retry</button>
              ) : system === null ? (
                <Skeleton className="h-8 w-24" />
              ) : (
                <p className="text-2xl font-semibold">{formatBytes(system.self.database_bytes)}</p>
              )}
            </CardContent>
          </Card>
        )}
      </div>

      {canHost && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between pb-2">
            <div>
            <CardTitle className="text-sm font-medium">Host resources</CardTitle>
              {system !== null && (
                <p className="text-muted-foreground text-xs">{collectionFreshnessLabel(system.freshness)}</p>
              )}
            </div>
            <Button variant="ghost" size="sm" onClick={() => onView('fleet')}>
              Fleet
            </Button>
          </CardHeader>
          <CardContent>
            {system === null && systemError !== null ? (
              <ErrorState title="Host resources unavailable" message={systemError} onRetry={() => void load()} />
            ) : system === null ? (
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
                  <dt className="text-muted-foreground text-xs"><button className="underline underline-offset-4" onClick={() => onView('images')}>Disk · manage storage</button></dt>
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
      )}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="flex flex-row items-center justify-between">
            <CardTitle className="text-sm font-medium">Services</CardTitle>
            <Button variant="ghost" size="sm" onClick={() => onView('services')}>
              View all
            </Button>
          </CardHeader>
          <CardContent className="space-y-1">
            {services === null && servicesError !== null && (
              <ErrorState title="Services unavailable" message={servicesError} onRetry={onRetryServices} />
            )}
            {services === null && servicesError === null && (
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
            {services !== null && servicesError !== null && (
              <div role="alert" className="border-destructive/40 mt-2 flex items-center justify-between rounded-md border p-2 text-xs">
                <span>Refresh failed. Showing last known services.</span>
                <Button variant="ghost" size="sm" onClick={onRetryServices}>Retry</Button>
              </div>
            )}
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
              <NotificationActivity activity={activity} />
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
