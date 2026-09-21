import { useEffect, useState } from 'react';

interface OperationEvent {
  type: string;
  service_id: string;
  message: string;
  at: string;
}

function parseOperationEvent(data: string): OperationEvent | null {
  try {
    const parsed = JSON.parse(data) as Partial<OperationEvent>;

    if (
      typeof parsed.type !== 'string' ||
      typeof parsed.service_id !== 'string' ||
      typeof parsed.message !== 'string' ||
      typeof parsed.at !== 'string'
    ) {
      return null;
    }

    return {
      type: parsed.type,
      service_id: parsed.service_id,
      message: parsed.message,
      at: parsed.at,
    };
  } catch {
    return null;
  }
}

function formatLine(event: OperationEvent): string {
  if (event.message === '') {
    return `[${event.type}]`;
  }

  return `[${event.type}] ${event.message}`;
}

const maxLines = 200;

export function useOperationEvents({
  serviceId,
  active,
  finishedType,
  operationNoun,
  onFinished,
  onStreamError,
}: {
  serviceId: string;
  active: boolean;
  finishedType: string;
  operationNoun: string;
  onFinished: () => void;
  onStreamError: () => void;
}): { lines: string[]; streamError: string | null; clear: () => void } {
  const [lines, setLines] = useState<string[]>([]);
  const [streamError, setStreamError] = useState<string | null>(null);

  useEffect(() => {
    if (!active) {
      return;
    }

    const source = new EventSource('/api/events', { withCredentials: true });

    source.onmessage = (event: MessageEvent) => {
      if (typeof event.data !== 'string') {
        return;
      }

      const parsed = parseOperationEvent(event.data);

      if (parsed === null || parsed.service_id !== serviceId) {
        return;
      }

      setLines((current) => [...current.slice(-(maxLines - 1)), formatLine(parsed)]);

      if (parsed.type === finishedType) {
        source.close();
        onFinished();
      }
    };

    source.onerror = () => {
      source.close();
      setStreamError(`Live progress disconnected before the ${operationNoun} finished.`);
      onStreamError();
    };

    return () => {
      source.close();
    };
  }, [active, finishedType, onFinished, onStreamError, operationNoun, serviceId]);

  return {
    lines,
    streamError,
    clear: () => {
      setLines([]);
      setStreamError(null);
    },
  };
}
