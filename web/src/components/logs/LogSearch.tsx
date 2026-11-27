import { useCallback, useEffect, useRef, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs';
import ErrorState from '../ErrorState.tsx';
import {
  fetchLogContext,
  fetchLogs,
  fetchLogStats,
  isUnauthorized,
  listSdkLogs,
  type LogContext,
  type LogLine,
  type LogStats,
  type SdkLogEntry,
} from '@/lib/api.ts';
import LogLineRow from './LogLineRow.tsx';
import SdkLogRow, { shortHex } from './SdkLogRow.tsx';

const limitOptions = ['50', '100', '200', '500'] as const;

const levelOptions = [
  { value: 'all', label: 'all levels' },
  { value: 'error', label: 'errors' },
  { value: 'error,warn', label: 'errors + warnings' },
  { value: 'warn', label: 'warnings' },
  { value: 'info', label: 'info' },
  { value: 'debug', label: 'debug' },
] as const;

const sdkLevelOptions = [
  ...levelOptions,
  { value: 'fatal', label: 'fatal' },
  { value: 'trace', label: 'trace' },
] as const;

type LevelFilter = (typeof levelOptions)[number]['value'] | 'fatal' | 'trace';

type Source = 'containers' | 'sdk';

interface SearchParams {
  q: string;
  after: string;
  before: string;
  limit: string;
  stream: string;
  level: string;
  source: Source;
  traceId: string;
}

function isSdkOnlyLevel(level: string): boolean {
  return level === 'fatal' || level === 'trace';
}

function localDateTime(date: Date): string {
  if (Number.isNaN(date.getTime())) return '';
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 23);
}

function toISO(value: string): string {
  return value === '' ? '' : new Date(value).toISOString();
}

export default function LogSearch({
  serviceId,
  initialAfter,
  initialBefore,
  onUnauthorized,
}: {
  serviceId: string;
  initialAfter?: string | null;
  initialBefore?: string | null;
  onUnauthorized: () => void;
}) {
  const [query, setQuery] = useState('');
  const [after, setAfter] = useState(() => initialAfter
    ? localDateTime(new Date(initialAfter))
    : localDateTime(new Date(Date.now() - 60 * 60_000)));
  const [before, setBefore] = useState(() => initialBefore ? localDateTime(new Date(initialBefore)) : '');
  const [limit, setLimit] = useState<(typeof limitOptions)[number]>('100');
  const [stream, setStream] = useState<'all' | 'stdout' | 'stderr'>('all');
  const [level, setLevel] = useState<LevelFilter>('all');
  const [source, setSource] = useState<Source>('containers');
  const [traceId, setTraceId] = useState('');
  const [lines, setLines] = useState<LogLine[] | null>(null);
  const [sdkLines, setSdkLines] = useState<SdkLogEntry[] | null>(null);
  const [stats, setStats] = useState<LogStats | null>(null);
  const [statsError, setStatsError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [context, setContext] = useState<LogContext | null>(null);
  const [contextError, setContextError] = useState<string | null>(null);
  const [contextOpen, setContextOpen] = useState(false);
  const [loadingContext, setLoadingContext] = useState(false);

  const queryRequest = useRef<AbortController | null>(null);
  const successfulSearch = useRef<SearchParams | null>(null);

  const executeSearch = useCallback(
    async (params: SearchParams) => {
      queryRequest.current?.abort();
      const controller = new AbortController();
      queryRequest.current = controller;
      const signal = controller.signal;
      setSearching(true);
      setLoadingOlder(false);
      setError(null);

      try {
        const bounds = { after: toISO(params.after), before: toISO(params.before) };
        if (params.source === 'sdk') {
          const fetched = await listSdkLogs(serviceId, {
            q: params.q,
            ...bounds,
            level: params.level === 'all' ? '' : params.level,
            trace_id: params.traceId,
            limit: Number(params.limit),
          }, signal);
          if (!signal.aborted) setSdkLines(fetched);
        } else {
          const fetched = await fetchLogs(serviceId, {
            q: params.q,
            ...bounds,
            stream: params.stream === 'all' ? '' : params.stream,
            level: params.level === 'all' ? '' : params.level,
            limit: Number(params.limit),
          }, signal);
          if (!signal.aborted) setLines(fetched);
        }

        if (!signal.aborted) successfulSearch.current = params;
      } catch (err) {
        if (signal.aborted) return;
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!signal.aborted) setSearching(false);
      }
    },
    [serviceId, onUnauthorized],
  );

  const search = useCallback(async () => {
    await executeSearch({
      q: query.trim(),
      after: after.trim(),
      before: before.trim(),
      limit,
      stream,
      level,
      source,
      traceId: traceId.trim(),
    });
  }, [executeSearch, query, after, before, limit, stream, level, source, traceId]);

  // Stream/level selects apply instantly (server-side); text inputs wait
  // for Search/Enter so typing never refires the query.
  const applyStream = (value: typeof stream) => {
    setStream(value);
    void executeSearch({
      q: query.trim(),
      after: after.trim(),
      before: before.trim(),
      limit,
      stream: value,
      level,
      source,
      traceId: traceId.trim(),
    });
  };

  const applyLevel = (value: LevelFilter) => {
    setLevel(value);
    void executeSearch({
      q: query.trim(),
      after: after.trim(),
      before: before.trim(),
      limit,
      stream,
      level: value,
      source,
      traceId: traceId.trim(),
    });
  };

  const applySource = (value: Source) => {
    setSource(value);
    let nextLevel = level;

    if (value === 'containers' && isSdkOnlyLevel(level)) {
      nextLevel = 'all';
      setLevel(nextLevel);
    }

    void executeSearch({
      q: query.trim(),
      after: after.trim(),
      before: before.trim(),
      limit,
      stream,
      level: nextLevel,
      source: value,
      traceId: traceId.trim(),
    });
  };

  const applyTrace = (id: string) => {
    setTraceId(id);
    void executeSearch({
      q: query.trim(),
      after: after.trim(),
      before: before.trim(),
      limit,
      stream,
      level,
      source,
      traceId: id,
    });
  };

  const clearTrace = () => {
    setTraceId('');
    void executeSearch({
      q: query.trim(),
      after: after.trim(),
      before: before.trim(),
      limit,
      stream,
      level,
      source,
      traceId: '',
    });
  };

  // Initial load per service only — typing in the filters must not
  // refire the search (the ref holds the latest closure instead).
  const searchRef = useRef(search);
  searchRef.current = search;

  useEffect(() => {
    void searchRef.current();
    return () => queryRequest.current?.abort();
  }, [serviceId]);

  const loadOlder = useCallback(async () => {
    const signal = queryRequest.current?.signal;
    const params = successfulSearch.current;
    if (signal === undefined || signal.aborted || params === null || params.source !== source) return;
    if (source === 'sdk') {
      if (sdkLines === null || sdkLines.length === 0) {
        return;
      }

      const oldest = sdkLines.reduce((a, b) => (a.ts < b.ts ? a : b)).ts;
      setLoadingOlder(true);
      setError(null);

      try {
        const fetched = await listSdkLogs(serviceId, {
          q: params.q,
          after: toISO(params.after),
          before: oldest,
          level: params.level === 'all' ? '' : params.level,
          trace_id: params.traceId,
          limit: Number(params.limit),
        }, signal);
        if (signal.aborted) return;
        const seen = new Set(sdkLines.map((entry) => entry.id));
        const fresh = fetched.filter((entry) => !seen.has(entry.id));
        setSdkLines([...sdkLines, ...fresh]);

        if (fresh.length === 0) {
          setBefore(localDateTime(new Date(oldest)));
        }
      } catch (err) {
        if (signal.aborted) return;
      if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!signal.aborted) setLoadingOlder(false);
      }

      return;
    }

    if (lines === null || lines.length === 0) {
      return;
    }

    const oldest = lines.reduce((a, b) => (a.ts < b.ts ? a : b)).ts;
    setLoadingOlder(true);
    setError(null);

    try {
      const fetched = await fetchLogs(serviceId, {
        q: params.q,
        after: toISO(params.after),
        before: oldest,
        stream: params.stream === 'all' ? '' : params.stream,
        level: params.level === 'all' ? '' : params.level,
        limit: Number(params.limit),
      }, signal);
      if (signal.aborted) return;
      const seen = new Set(lines.map((line) => line.id));
      const fresh = fetched.filter((line) => !seen.has(line.id));
      setLines([...lines, ...fresh]);

      if (fresh.length === 0) {
        setBefore(localDateTime(new Date(oldest)));
      }
    } catch (err) {
      if (signal.aborted) return;
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (!signal.aborted) setLoadingOlder(false);
    }
  }, [lines, sdkLines, serviceId, source, onUnauthorized]);

  const openContext = useCallback(
    async (line: LogLine) => {
      setContextOpen(true);
      setContext(null);
      setContextError(null);
      setLoadingContext(true);

      try {
        setContext(await fetchLogContext(line.id));
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setContextError(err instanceof Error ? err.message : String(err));
      } finally {
        setLoadingContext(false);
      }
    },
    [onUnauthorized],
  );

  const applyRange = (minutes: number) => {
    const start = localDateTime(new Date(Date.now() - minutes * 60_000));
    setAfter(start);
    setBefore('');
    void executeSearch({ q: query.trim(), after: start, before: '', limit, stream, level, source, traceId: traceId.trim() });
  };

  async function showStats() {
    setStatsError(null);
    try {
      setStats(await fetchLogStats(serviceId));
    } catch (err) {
      if (isUnauthorized(err)) onUnauthorized();
      else setStatsError(err instanceof Error ? err.message : String(err));
    }
  }

  const visibleLevels = source === 'sdk' ? sdkLevelOptions : levelOptions;
  const results = source === 'sdk' ? sdkLines : lines;

  return (
    <div className="space-y-4">
      <Tabs value={source} onValueChange={(value) => applySource(value as Source)}>
        <TabsList aria-label="Log source">
          <TabsTrigger value="containers">Containers</TabsTrigger>
          <TabsTrigger value="sdk">Application</TabsTrigger>
        </TabsList>
      </Tabs>

      <Card>
        <CardContent className="space-y-3 pt-4">
          <div className="flex flex-col gap-2 lg:flex-row">
            <Input
              placeholder="Full-text search…"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  void search();
                }
              }}
              className="lg:flex-1"
              aria-label="Search log text"
            />
            <label className="text-muted-foreground space-y-1 text-xs lg:w-52">
              <span>From (local time)</span>
              <Input
                type="datetime-local"
                step="0.001"
                value={after}
                onChange={(event) => setAfter(event.target.value)}
                aria-label="From (local time)"
              />
            </label>
            <label className="text-muted-foreground space-y-1 text-xs lg:w-52">
              <span>To (local time)</span>
              <Input
                type="datetime-local"
                step="0.001"
                value={before}
                onChange={(event) => setBefore(event.target.value)}
                aria-label="To (local time)"
              />
            </label>
          </div>
          <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Quick time ranges">
            <Button variant="outline" size="sm" onClick={() => applyRange(15)}>Last 15m</Button>
            <Button variant="outline" size="sm" onClick={() => applyRange(60)}>Last hour</Button>
            <Button variant="outline" size="sm" onClick={() => applyRange(1_440)}>Last 24h</Button>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Select value={limit} onValueChange={(value) => setLimit(value as typeof limit)}>
              <SelectTrigger className="w-32" aria-label="Result limit">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {limitOptions.map((option) => (
                  <SelectItem key={option} value={option}>
                    {option} lines
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {source === 'containers' && (
              <Select value={stream} onValueChange={(value) => applyStream(value as typeof stream)}>
                <SelectTrigger className="w-32" aria-label="Stream filter">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">all streams</SelectItem>
                  <SelectItem value="stdout">stdout</SelectItem>
                  <SelectItem value="stderr">stderr</SelectItem>
                </SelectContent>
              </Select>
            )}
            <Select value={level} onValueChange={(value) => applyLevel(value as LevelFilter)}>
              <SelectTrigger className="w-40" aria-label="Level filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {visibleLevels.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button onClick={() => void search()} disabled={searching}>
              {searching ? 'Searching…' : 'Search'}
            </Button>
            {source === 'sdk' && traceId !== '' && (
              <span className="inline-flex items-center gap-1.5">
                <Badge
                  variant="outline"
                  className="font-mono text-[11px]"
                  title={`Trace filter: ${traceId}`}
                >
                  trace:{shortHex(traceId)}
                </Badge>
                <Button variant="ghost" size="sm" onClick={clearTrace}>
                  Clear
                </Button>
              </span>
            )}
            {source === 'containers' && stats === null && (
              <Button variant="ghost" size="sm" onClick={() => void showStats()}>Show storage stats</Button>
            )}
            {source === 'containers' && stats !== null && (
              <p className="text-muted-foreground ml-auto text-xs">
                {stats.lines.toLocaleString()} stored
                {stats.drops > 0 && ` · ${stats.drops} dropped`}
                {stats.truncations > 0 && ` · ${stats.truncations} truncated`}
              </p>
            )}
          </div>
          {source === 'containers' && statsError !== null && (
            <p className="text-muted-foreground text-xs" role="status">Storage stats unavailable: {statsError}</p>
          )}
        </CardContent>
      </Card>

      {error !== null && (
        <ErrorState title="Couldn't search logs" message={error} onRetry={() => void search()} />
      )}

      {results === null && error === null && (
        <div className="space-y-2" aria-label="Loading log lines">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      )}

      {results !== null && source === 'containers' && (
        <Card>
          <CardContent className="px-0 py-2">
            {lines !== null && lines.length === 0 ? (
              <p className="text-muted-foreground px-4 py-6 text-center text-sm">
                No lines match. Loosen the filters or widen the window.
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {(lines ?? []).map((line) => (
                  <LogLineRow key={line.id} line={line} anchor={false} onSelect={openContext} />
                ))}
              </ul>
            )}
            {(lines ?? []).length > 0 && (
              <div className="flex justify-center border-t px-4 py-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void loadOlder()}
                  disabled={loadingOlder || searching}
                >
                  {loadingOlder ? 'Loading…' : 'Load older'}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {results !== null && source === 'sdk' && (
        <Card>
          <CardContent className="px-0 py-2">
            {sdkLines !== null && sdkLines.length === 0 ? (
              traceId !== '' ? (
                <div className="flex flex-col items-center gap-2 px-4 py-6 text-center">
                  <p className="text-muted-foreground text-sm">
                    No application logs for this trace.
                  </p>
                  <Button variant="outline" size="sm" onClick={clearTrace}>
                    Clear trace filter
                  </Button>
                </div>
              ) : (
                <div className="flex flex-col items-center gap-1 px-4 py-6 text-center">
                  <p className="text-muted-foreground text-sm">
                    No application logs yet. Loosen the filters — or instrument the app with the
                    SDK logger.
                  </p>
                  <a
                    href="/docs/sdk-logging"
                    className="text-primary text-sm underline underline-offset-4"
                  >
                    SDK logging setup guide
                  </a>
                </div>
              )
            ) : (
              <ul className="divide-y divide-border">
                {(sdkLines ?? []).map((entry) => (
                  <SdkLogRow
                    key={entry.id}
                    entry={entry}
                    onTraceSelect={applyTrace}
                  />
                ))}
              </ul>
            )}
            {(sdkLines ?? []).length > 0 && (
              <div className="flex justify-center border-t px-4 py-3">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void loadOlder()}
                  disabled={loadingOlder || searching}
                >
                  {loadingOlder ? 'Loading…' : 'Load older'}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      <Sheet open={contextOpen} onOpenChange={setContextOpen}>
        <SheetContent className="overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle>Surrounding context</SheetTitle>
            <SheetDescription>
              20 lines before and after the selected line, same container.
            </SheetDescription>
          </SheetHeader>
          {loadingContext && (
            <div className="space-y-2">
              <Skeleton className="h-6 w-full" />
              <Skeleton className="h-6 w-full" />
              <Skeleton className="h-6 w-full" />
            </div>
          )}
          {contextError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {contextError}
            </p>
          )}
          {context !== null && (
            <ul className="divide-y divide-border rounded-md border">
              {context.before.map((line) => (
                <LogLineRow key={line.id} line={line} anchor={false} />
              ))}
              <LogLineRow line={context.anchor} anchor />
              {context.after.map((line) => (
                <LogLineRow key={line.id} line={line} anchor={false} />
              ))}
            </ul>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
