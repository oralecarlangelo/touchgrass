import { useCallback, useEffect, useRef, useState } from 'react';
import {
  fetchLogContext,
  fetchLogs,
  isUnauthorized,
  type LogContext,
  type LogLine,
  type ServiceView,
} from '../lib/api.ts';

const pollIntervalMs = 5_000;
const tailLimit = 200;

function formatTime(value: string): string {
  const parsed = new Date(value);

  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return parsed.toLocaleTimeString();
}

const inputClass =
  'rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-sm text-gray-900';
const buttonClass =
  'rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50';

export default function Logs({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState('');
  const [query, setQuery] = useState('');
  const [submitted, setSubmitted] = useState('');
  const [lines, setLines] = useState<LogLine[] | null>(null);
  const [detail, setDetail] = useState<LogContext | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [live, setLive] = useState(true);
  const newestRef = useRef<string>('');

  const effective = serviceId !== '' ? serviceId : (services[0]?.id ?? '');

  const load = useCallback(
    async (after?: string) => {
      if (effective === '') {
        return;
      }

      // Live-append keeps the rendered window stable while lines stream in.
      const appending = after !== undefined && after !== '';

      if (!appending) {
        setLoading(true);
      }

      setError(null);

      try {
        const fetched = await fetchLogs(effective, {
          q: submitted,
          after,
          limit: tailLimit,
        });

        if (appending) {
          setLines((current) => {
            if (current === null) {
              return fetched;
            }

            const seen = new Set(current.map((line) => line.id));
            const fresh = fetched.filter((line) => !seen.has(line.id));

            return [...fresh, ...current].slice(0, tailLimit);
          });
        } else {
          setLines(fetched);
        }

        if (fetched.length > 0 && (after !== undefined || submitted === '')) {
          newestRef.current = fetched[0].ts;
        }
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setLoading(false);
      }
    },
    [effective, submitted, onUnauthorized],
  );

  useEffect(() => {
    newestRef.current = '';
    setLines(null);
    setDetail(null);
    void load();
  }, [load]);

  useEffect(() => {
    if (!live || effective === '' || submitted !== '') {
      return;
    }

    const timer = setInterval(() => {
      void load(newestRef.current);
    }, pollIntervalMs);

    return () => clearInterval(timer);
  }, [live, effective, submitted, load]);

  const loadDetail = useCallback(
    async (id: number) => {
      setError(null);
      setDetail(null);

      try {
        setDetail(await fetchLogContext(id));
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setError(err instanceof Error ? err.message : String(err));
      }
    },
    [onUnauthorized],
  );

  return (
    <div className="space-y-4">
      <section aria-label="logs" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900">Logs</h2>
          <label className="ml-auto text-sm text-gray-700">
            Service{' '}
            <select
              value={effective}
              onChange={(event) => setServiceId(event.target.value)}
              className={inputClass}
            >
              {services.map((service) => (
                <option key={service.id} value={service.id}>
                  {service.name}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => void load()}
            disabled={loading || effective === ''}
            aria-label="Refresh logs"
            className={buttonClass}
          >
            {loading ? 'Checking…' : 'Refresh'}
          </button>
          <button
            type="button"
            onClick={() => setLive((value) => !value)}
            aria-pressed={live}
            aria-label={live ? 'Pause live tail' : 'Resume live tail'}
            disabled={submitted !== ''}
            title={submitted !== '' ? 'Live tail pauses while searching' : undefined}
            className={buttonClass}
          >
            {live ? '● Live' : '○ Paused'}
          </button>
        </div>

        <form
          className="mt-3 flex flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            setSubmitted(query.trim());
          }}
        >
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search log text…"
            aria-label="Search logs"
            className={`${inputClass} min-w-0 flex-1`}
          />
          <button type="submit" disabled={effective === ''} className={buttonClass}>
            Search
          </button>
          {submitted !== '' && (
            <button
              type="button"
              onClick={() => {
                setQuery('');
                setSubmitted('');
              }}
              aria-label="Clear log search"
              className={buttonClass}
            >
              Clear
            </button>
          )}
        </form>

        {error !== null && (
          <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
            <p className="text-sm text-red-700">{error}</p>
          </div>
        )}

        {lines === null ? (
          <p className="mt-3 text-sm text-gray-500">Loading logs…</p>
        ) : lines.length === 0 ? (
          <p className="mt-3 text-sm text-gray-500">No lines yet — the collector polls every 5s.</p>
        ) : (
          <ul
            aria-label="log lines"
            className="mt-3 max-h-96 space-y-0.5 overflow-y-auto rounded-md bg-gray-900 p-3 font-mono text-xs"
          >
            {lines.map((line) => (
              <li key={line.id}>
                <button
                  type="button"
                  onClick={() => void loadDetail(line.id)}
                  aria-label={`Open log context for line ${line.id}`}
                  className="flex w-full gap-2 rounded-sm px-1 py-0.5 text-left hover:bg-gray-800"
                >
                  <span className="shrink-0 text-gray-500">{formatTime(line.ts)}</span>
                  <span
                    className={line.stream === 'stderr' ? 'shrink-0 text-red-400' : 'shrink-0 text-gray-500'}
                  >
                    {line.stream}
                  </span>
                  <span className="break-all text-gray-100">{line.line}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      {detail !== null && (
        <section
          aria-label="log context"
          className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
        >
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-semibold text-gray-900">Line context</h2>
            <button
              type="button"
              onClick={() => setDetail(null)}
              aria-label="Close log context"
              className={`${buttonClass} ml-auto`}
            >
              Close
            </button>
          </div>

          <p className="mt-1 font-mono text-xs text-gray-500">
            {detail.anchor.container} · {detail.anchor.stream} · {formatTime(detail.anchor.ts)}
          </p>

          <div className="mt-2 space-y-0.5 rounded-md bg-gray-900 p-3 font-mono text-xs">
            {detail.before.map((line) => (
              <p key={line.id} className="break-all text-gray-400">
                <span className="text-gray-600">{formatTime(line.ts)} </span>
                {line.line}
              </p>
            ))}
            <p className="break-all rounded-sm bg-gray-800 px-1 py-0.5 text-yellow-200">
              <span className="text-yellow-500">{formatTime(detail.anchor.ts)} </span>
              {detail.anchor.line}
            </p>
            {detail.after.map((line) => (
              <p key={line.id} className="break-all text-gray-400">
                <span className="text-gray-600">{formatTime(line.ts)} </span>
                {line.line}
              </p>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}
