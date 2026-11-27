import { useCallback, useEffect, useState } from 'react';

import { isUnauthorized } from './api.ts';

interface PanelState<T> {
  scope: string;
  data: T | null;
  error: string | null;
  loading: boolean;
}

// One panel owns its read and the last result for the current scope.
export function usePanelRead<T>(
  scope: string,
  read: ((signal: AbortSignal) => Promise<T>) | null,
  refresh: number,
  onUnauthorized: () => void,
) {
  const [state, setState] = useState<PanelState<T> | null>(null);
  const [attempt, setAttempt] = useState(0);
  const retry = useCallback(() => setAttempt((value) => value + 1), []);

  useEffect(() => {
    if (read === null) return;

    const controller = new AbortController();
    const signal = controller.signal;
    setState((current) => ({
      scope,
      data: current?.scope === scope ? current.data : null,
      error: null,
      loading: true,
    }));

    void read(signal).then((data) => {
      if (!signal.aborted) setState({ scope, data, error: null, loading: false });
    }, (error: unknown) => {
      if (signal.aborted) return;
      if (isUnauthorized(error)) {
        onUnauthorized();
        return;
      }

      const message = error instanceof Error ? error.message : String(error);
      setState((current) => ({
        scope,
        data: current?.scope === scope ? current.data : null,
        error: message,
        loading: false,
      }));
    });

    return () => controller.abort();
  }, [attempt, onUnauthorized, read, refresh, scope]);

  return {
    ...(state?.scope === scope && read !== null
      ? state
      : { data: null, error: null, loading: read !== null }),
    retry,
  };
}
