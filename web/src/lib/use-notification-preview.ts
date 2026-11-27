import { useCallback, useEffect } from 'react';

import { fetchNotifications } from './api.ts';
import { usePanelRead } from './use-panel-read.ts';

const pollIntervalMs = 30_000;
export const notificationPreviewLimit = 5;

// Dashboard and the bell share a session-scoped notification projection.
export function useNotificationPreview(
  scope: string,
  canRead: boolean,
  isDashboard: boolean,
  onUnauthorized: () => void,
) {
  const limit = isDashboard ? 100 : notificationPreviewLimit;
  const read = useCallback((signal: AbortSignal) => fetchNotifications('', limit, signal), [limit]);
  const preview = usePanelRead(scope, canRead ? read : null, 0, onUnauthorized);
  const { loading, retry } = preview;

  useEffect(() => {
    if (!canRead || loading) return;

    const refreshVisible = () => { if (!document.hidden) retry(); };
    const timer = window.setTimeout(refreshVisible, pollIntervalMs);
    document.addEventListener('visibilitychange', refreshVisible);

    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', refreshVisible);
    };
  }, [canRead, loading, retry]);

  return preview;
}

export type NotificationPreview = ReturnType<typeof useNotificationPreview>;
