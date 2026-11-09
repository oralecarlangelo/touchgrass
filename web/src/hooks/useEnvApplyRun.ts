import { useCallback, useEffect, useRef, useState } from 'react';

import { fetchEnvRun, isUnauthorized } from '@/lib/api.ts';
import {
  initialRunState,
  mergeRun,
  parseEnvApplyEvent,
  reduceRunEvent,
  type ApplyRunState,
} from '@/lib/env.ts';

// Polling cadence: slow while the SSE stream is healthy (it only backfills
// what the stream might have dropped), fast once the stream is gone.
const pollWithStreamMs = 5_000;
const pollWithoutStreamMs = 2_000;

export interface EnvApplyRunHandle {
  run: ApplyRunState | null;
  streamLost: boolean;
  start: (runId: string) => void;
  dismiss: () => void;
}

// useEnvApplyRun follows one env apply/rollback run: SSE `env.apply` events
// drive the stepper, GET /env/runs/{id} backfills and takes over if the
// stream disconnects. `onSettled` fires once when the run reaches done/failed.
export function useEnvApplyRun({
  serviceId,
  onSettled,
  onUnauthorized,
}: {
  serviceId: string;
  onSettled: (run: ApplyRunState) => void;
  onUnauthorized: () => void;
}): EnvApplyRunHandle {
  const [run, setRun] = useState<ApplyRunState | null>(null);
  const [streamLost, setStreamLost] = useState(false);
  const settledRef = useRef<string | null>(null);
  const reconciledRef = useRef<string | null>(null);
  const runRef = useRef<ApplyRunState | null>(null);

  runRef.current = run;

  const start = useCallback((runId: string) => {
    settledRef.current = null;
    reconciledRef.current = null;
    setStreamLost(false);
    setRun(initialRunState(runId));
  }, []);

  const dismiss = useCallback(() => {
    setRun(null);
    setStreamLost(false);
  }, []);

  // Once the stream reports a terminal state, read the persisted record once
  // so a run that finished before the stream attached still shows every step.
  useEffect(() => {
    if (run === null || run.status === 'running' || reconciledRef.current === run.runId) {
      return;
    }

    reconciledRef.current = run.runId;
    const { runId } = run;

    void fetchEnvRun(serviceId, runId)
      .then((fetched) => {
        setRun((current) => (current === null || current.runId !== runId ? current : mergeRun(current, fetched)));
      })
      .catch(() => {
        // The stream already told us the outcome; the record is a bonus.
      });
  }, [run, serviceId]);

  // Fire onSettled exactly once per run.
  useEffect(() => {
    if (run === null || run.status === 'running' || settledRef.current === run.runId) {
      return;
    }

    settledRef.current = run.runId;
    onSettled(run);
  }, [run, onSettled]);

  const active = run !== null && run.status === 'running';
  const runId = run?.runId ?? null;

  // SSE stream.
  useEffect(() => {
    if (!active || runId === null) {
      return;
    }

    const source = new EventSource('/api/events', { withCredentials: true });
    const handle = (event: MessageEvent) => {
      if (typeof event.data !== 'string') {
        return;
      }

      const parsed = parseEnvApplyEvent(event.data);

      if (parsed === null || parsed.service_id !== serviceId || parsed.run_id !== runId) {
        return;
      }

      setRun((current) => (current === null ? current : reduceRunEvent(current, parsed)));
    };

    source.onmessage = handle;
    source.addEventListener('env.apply', handle as EventListener);
    source.onerror = () => {
      source.close();
      setStreamLost(true);
    };

    return () => {
      source.close();
    };
  }, [active, runId, serviceId]);

  // Poll fallback.
  useEffect(() => {
    if (!active || runId === null) {
      return;
    }

    let cancelled = false;
    const poll = async () => {
      try {
        const fetched = await fetchEnvRun(serviceId, runId);

        if (!cancelled) {
          setRun((current) => (current === null ? current : mergeRun(current, fetched)));
        }
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
        }
        // Other errors: the next tick retries; SSE may still be delivering.
      }
    };

    const timer = setInterval(() => void poll(), streamLost ? pollWithoutStreamMs : pollWithStreamMs);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [active, runId, serviceId, streamLost, onUnauthorized]);

  return { run, streamLost, start, dismiss };
}
