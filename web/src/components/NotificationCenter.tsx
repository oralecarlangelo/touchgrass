import { useCallback, useEffect, useState } from 'react';
import {
  fetchNotifications,
  isUnauthorized,
  markNotificationRead,
  markNotificationsRead,
  type Notification,
  type ServiceView,
} from '../lib/api.ts';

function formatTimestamp(value: string): string {
  const parsed = new Date(value);

  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return parsed.toLocaleString();
}

export default function NotificationCenter({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState('');
  const [notifications, setNotifications] = useState<Notification[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const result = await fetchNotifications(serviceId);
      setNotifications(result.notifications);
      setUnread(result.unread);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [serviceId, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  async function handleMarkRead(id: number): Promise<void> {
    setError(null);

    try {
      await markNotificationRead(id);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleMarkAllRead(): Promise<void> {
    setError(null);

    try {
      await markNotificationsRead(serviceId);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="space-y-4">
      <section
        aria-label="notification center"
        className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
      >
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900">
            Notifications{unread > 0 && <span className="ml-2 text-sm font-medium text-gray-500">({unread} unread)</span>}
          </h2>
          <label className="ml-auto text-sm text-gray-700">
            Service{' '}
            <select
              value={serviceId}
              onChange={(event) => setServiceId(event.target.value)}
              className="rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-sm text-gray-900"
            >
              <option value="">all services</option>
              {services.map((service) => (
                <option key={service.id} value={service.id}>
                  {service.name}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => void handleMarkAllRead()}
            disabled={loading || unread === 0}
            aria-label="Mark all notifications read"
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            Mark all read
          </button>
          <button
            type="button"
            onClick={() => void load()}
            disabled={loading}
            aria-label="Refresh notifications"
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            {loading ? 'Checking…' : 'Refresh'}
          </button>
        </div>

        <p className="mt-1 text-sm text-gray-600">
          Deploy start/finish/failure and alert breaches land here.
        </p>

        {error !== null && (
          <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
            <p className="text-sm text-red-700">{error}</p>
          </div>
        )}

        {notifications === null ? (
          <p className="mt-3 text-sm text-gray-500">Loading notifications…</p>
        ) : notifications.length === 0 ? (
          <p className="mt-3 text-sm text-gray-500">All quiet — grass: touched. 🌱</p>
        ) : (
          <ul className="mt-3 divide-y divide-gray-100">
            {notifications.map((notification) => {
              const isUnread = notification.read_at === null;

              return (
                <li key={notification.id} className="py-2 text-sm">
                  <div className="flex flex-wrap items-center gap-2">
                    <span
                      aria-label={isUnread ? 'Unread' : 'Read'}
                      className={`inline-block h-2 w-2 rounded-full ${isUnread ? 'bg-blue-600' : 'bg-gray-200'}`}
                    />
                    <span className={`text-gray-900 ${isUnread ? 'font-semibold' : ''}`}>
                      {notification.title}
                    </span>
                    <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700">
                      {notification.kind}
                    </span>
                    <span className="text-xs text-gray-500">{notification.service_id}</span>
                    <span className="ml-auto font-mono text-xs text-gray-500">
                      {formatTimestamp(notification.created_at)}
                    </span>
                  </div>
                  {notification.body !== '' && (
                    <p className="mt-0.5 text-sm text-gray-500">{notification.body}</p>
                  )}
                  {isUnread && (
                    <button
                      type="button"
                      onClick={() => void handleMarkRead(notification.id)}
                      aria-label={`Mark notification ${notification.id} read`}
                      className="mt-1 text-sm text-blue-700 hover:underline"
                    >
                      Mark read
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
