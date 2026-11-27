import { useCallback, useMemo, useState } from 'react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from './ErrorState.tsx';
import {
  fetchNotifications,
  isUnauthorized,
  markNotificationRead,
  markNotificationsRead,
  type Notification,
  type ServiceView,
} from '@/lib/api.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';
import { notificationFindingID } from '@/lib/security.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

const pageSize = 20;

function groupNotifications(items: Notification[]): Notification[][] {
  const bySignal = new Map<string, Notification[]>();

  for (const item of items) {
    const finding = item.kind === 'security' ? notificationFindingID(item.body) : null;
    const key = JSON.stringify([item.kind, item.service_id, finding ?? [item.title, item.body]]);
    const group = bySignal.get(key);
    if (group === undefined) bySignal.set(key, [item]);
    else group.push(item);
  }

  const unread = (group: Notification[]) => group.some((item) => item.read_at === null);
  return [...bySignal.values()].sort((a, b) => Number(unread(b)) - Number(unread(a)));
}

export default function NotificationsScreen({
  services,
  onUnauthorized,
  isAdmin,
  onReadChange,
  onSelectSecurityFinding,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
  isAdmin: boolean;
  onReadChange: () => void;
  onSelectSecurityFinding: (id: number) => void;
}) {
  const [serviceId, setServiceId] = useState('all');
  const [kind, setKind] = useState('all');
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [page, setPage] = useState(0);

  const effectiveServiceId = serviceId === 'all' ? '' : serviceId;
  const readFeed = useCallback(
    (signal: AbortSignal) => fetchNotifications(effectiveServiceId, 100, signal),
    [effectiveServiceId],
  );
  const { data, error, retry } = usePanelRead(effectiveServiceId, readFeed, 0, onUnauthorized);
  const notes = data?.notifications ?? null;
  const unread = data?.unread ?? 0;

  const kinds = useMemo(() => {
    const seen = new Set<string>();

    for (const item of notes ?? []) {
      seen.add(item.kind);
    }

    return [...seen].sort();
  }, [notes]);

  const filtered = useMemo(
    () =>
      (notes ?? []).filter(
        (item) =>
          (kind === 'all' || item.kind === kind) && (!unreadOnly || item.read_at === null),
      ),
    [notes, kind, unreadOnly],
  );

  const groups = useMemo(() => groupNotifications(filtered), [filtered]);
  const pageCount = Math.max(1, Math.ceil(groups.length / pageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = groups.slice(safePage * pageSize, safePage * pageSize + pageSize);

  async function handleMarkRead(id: number): Promise<void> {
    try {
      await markNotificationRead(id);
      onReadChange();
      retry();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleMarkAllRead(): Promise<void> {
    try {
      const result = await markNotificationsRead(effectiveServiceId);
      toast.success(`Marked ${result.marked} as read.`);
      onReadChange();
      retry();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  function notificationRow(item: Notification) {
    return (
      <li key={item.id} className="flex items-start gap-3 px-4 py-3">
        {!item.read_at && (
          <span
            className="bg-primary mt-1.5 h-2 w-2 shrink-0 rounded-full"
            aria-label="Unread"
          />
        )}
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{item.title}</p>
          {item.kind === 'security' && notificationFindingID(item.body) !== null && (
            <button
              type="button"
              className="text-primary text-xs underline underline-offset-4"
              onClick={() => onSelectSecurityFinding(notificationFindingID(item.body) as number)}
            >
              Open finding #{notificationFindingID(item.body)}
            </button>
          )}
          {item.body !== '' && (
            <p className="text-muted-foreground mt-0.5 text-sm break-words">
              {item.body}
            </p>
          )}
          <p className="text-muted-foreground mt-1 text-xs">
            <Badge variant="outline" className="mr-1.5 text-[10px]">
              {item.kind}
            </Badge>
            <span className="font-mono">{item.service_id}</span> · {timeAgo(item.created_at)}{' '}
            <span className="hidden sm:inline">({formatTime(item.created_at)})</span>
          </p>
        </div>
        {item.read_at === null && isAdmin && (
          <Button variant="ghost" size="sm" onClick={() => void handleMarkRead(item.id)}>
            Mark read
          </Button>
        )}
      </li>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">
          Notifications
          {unread > 0 && (
            <Badge variant="destructive" className="ml-2">
              {unread} unread
            </Badge>
          )}
        </h1>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Select
            value={serviceId}
            onValueChange={(value) => {
              setServiceId(value);
              setPage(0);
            }}
          >
            <SelectTrigger className="w-48" aria-label="Service filter">
              <SelectValue />
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
          <Select
            value={kind}
            onValueChange={(value) => {
              setKind(value);
              setPage(0);
            }}
          >
            <SelectTrigger className="w-40" aria-label="Kind filter">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All kinds</SelectItem>
              {kinds.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-2">
            <Checkbox
              id="unread-only"
              checked={unreadOnly}
              onCheckedChange={(checked) => {
                setUnreadOnly(checked === true);
                setPage(0);
              }}
            />
            <Label htmlFor="unread-only" className="font-normal">
              Unread only
            </Label>
          </div>
          {isAdmin && (
            <Button variant="outline" size="sm" onClick={() => void handleMarkAllRead()}>
              Mark all read
            </Button>
          )}
        </div>
      </div>

      {error && <ErrorState title="Couldn't load notifications" message={error} onRetry={retry} />}
      {notes === null ? !error && (
        <div className="space-y-2" aria-label="Loading notifications">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : (
        <Card>
          <CardContent className="px-0 py-1">
            {rows.length === 0 ? (
              <p className="text-muted-foreground px-4 py-8 text-center text-sm">
                No notifications match these filters.
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {rows.map((group) => {
                  const newest = group[0];
                  if (group.length === 1) return notificationRow(newest);

                  const oldest = group[group.length - 1];
                  const unreadCount = group.filter((item) => item.read_at === null).length;
                  return (
                    <li key={newest.id} className="px-4 py-2">
                      <details>
                        <summary className="cursor-pointer space-x-2 py-2 text-sm">
                          <span className="font-medium">{newest.title}</span>
                          <Badge variant="secondary">{group.length} occurrences</Badge>
                          {unreadCount > 0 && <Badge variant="outline">{unreadCount} unread</Badge>}
                          <span className="text-muted-foreground text-xs">
                            Earliest shown {timeAgo(oldest.created_at)} · Last {timeAgo(newest.created_at)}
                          </span>
                        </summary>
                        <ul className="divide-y divide-border">
                          {group.map(notificationRow)}
                        </ul>
                      </details>
                    </li>
                  );
                })}
              </ul>
            )}
          </CardContent>
        </Card>
      )}

      <div className="flex items-center justify-between text-sm">
        <p className="text-muted-foreground">
          {filtered.length} notification{filtered.length === 1 ? '' : 's'}
        </p>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" disabled={safePage === 0} onClick={() => setPage(safePage - 1)}>
            Previous
          </Button>
          <span className="text-muted-foreground">
            {safePage + 1} / {pageCount}
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={safePage + 1 >= pageCount}
            onClick={() => setPage(safePage + 1)}
          >
            Next
          </Button>
        </div>
      </div>
    </div>
  );
}
