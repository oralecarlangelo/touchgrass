import { useCallback, useEffect, useState } from 'react';
import { Bell } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import {
  fetchNotifications,
  isUnauthorized,
  markNotificationsRead,
  type Notification,
} from '@/lib/api.ts';
import { timeAgo } from '@/lib/format.ts';

const pollIntervalMs = 30_000;
const previewLimit = 5;

export default function NotificationBell({
  onViewAll,
  onUnauthorized,
}: {
  onViewAll: () => void;
  onUnauthorized: () => void;
}) {
  const [items, setItems] = useState<Notification[]>([]);
  const [unread, setUnread] = useState(0);

  const load = useCallback(async () => {
    try {
      const fetched = await fetchNotifications('', previewLimit);
      setItems(fetched.notifications);
      setUnread(fetched.unread);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
      }
      // Otherwise stay silent: the bell is ambient, the page shows errors.
    }
  }, [onUnauthorized]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), pollIntervalMs);

    return () => clearInterval(timer);
  }, [load]);

  const markAll = useCallback(async () => {
    try {
      await markNotificationsRead('');
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
      }
    }
  }, [load, onUnauthorized]);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`Notifications, ${unread} unread`}>
          <span className="relative">
            <Bell className="h-4 w-4" aria-hidden />
            {unread > 0 && (
              <span
                aria-hidden
                className="bg-destructive text-destructive-foreground absolute -top-2 -right-2 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-medium"
              >
                {unread > 9 ? '9+' : unread}
              </span>
            )}
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-80">
        <DropdownMenuLabel className="flex items-center">
          Notifications
          {unread > 0 && (
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto h-7 text-xs"
              onClick={() => void markAll()}
            >
              Mark all read
            </Button>
          )}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {items.length === 0 && (
          <p className="text-muted-foreground px-2 py-4 text-center text-sm">
            Quiet. Prod is covered.
          </p>
        )}
        {items.slice(0, previewLimit).map((item) => (
          <DropdownMenuItem key={item.id} onSelect={onViewAll} className="items-start gap-2">
            {!item.read_at && (
              <span className="bg-primary mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full" aria-hidden />
            )}
            <span className="min-w-0">
              <span className="block truncate text-sm font-medium">{item.title}</span>
              <span className="text-muted-foreground block text-xs">
                {item.service_id} · {timeAgo(item.created_at)}
              </span>
            </span>
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onViewAll} className="justify-center text-sm font-medium">
          View all
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
