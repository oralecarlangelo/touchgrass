import { useCallback, useEffect, useMemo, useState } from 'react';
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

const pageSize = 20;

export default function NotificationsScreen({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [notes, setNotes] = useState<Notification[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [serviceId, setServiceId] = useState('all');
  const [kind, setKind] = useState('all');
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [page, setPage] = useState(0);

  const load = useCallback(async () => {
    setError(null);

    try {
      const fetched = await fetchNotifications(serviceId === 'all' ? '' : serviceId);
      setNotes(fetched.notifications);
      setUnread(fetched.unread);
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

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = filtered.slice(safePage * pageSize, safePage * pageSize + pageSize);

  async function handleMarkRead(id: number): Promise<void> {
    try {
      await markNotificationRead(id);
      setNotes((current) =>
        (current ?? []).map((item) =>
          item.id === id ? { ...item, read_at: new Date().toISOString() } : item,
        ),
      );
      setUnread((current) => Math.max(0, current - 1));
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
      const result = await markNotificationsRead(serviceId === 'all' ? '' : serviceId);
      toast.success(`Marked ${result.marked} as read.`);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  if (error !== null) {
    return <ErrorState title="Couldn't load notifications" message={error} onRetry={() => void load()} />;
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
          <Button variant="outline" size="sm" onClick={() => void handleMarkAllRead()}>
            Mark all read
          </Button>
        </div>
      </div>

      {notes === null ? (
        <div className="space-y-2" aria-label="Loading notifications">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : (
        <Card>
          <CardContent className="px-0 py-1">
            {rows.length === 0 ? (
              <p className="text-muted-foreground px-4 py-8 text-center text-sm">
                Quiet. Prod is covered.
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {rows.map((item) => (
                  <li key={item.id} className="flex items-start gap-3 px-4 py-3">
                    {!item.read_at && (
                      <span
                        className="bg-primary mt-1.5 h-2 w-2 shrink-0 rounded-full"
                        aria-label="Unread"
                      />
                    )}
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium">{item.title}</p>
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
                    {item.read_at === null && (
                      <Button variant="ghost" size="sm" onClick={() => void handleMarkRead(item.id)}>
                        Mark read
                      </Button>
                    )}
                  </li>
                ))}
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
