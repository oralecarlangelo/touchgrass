import { useCallback, useEffect, useRef, useState } from 'react';

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
import ErrorState from '../ErrorState.tsx';
import {
  fetchLogContext,
  fetchLogs,
  fetchLogStats,
  isUnauthorized,
  type LogContext,
  type LogLine,
  type LogStats,
} from '@/lib/api.ts';
import LogLineRow from './LogLineRow.tsx';

const limitOptions = ['50', '100', '200', '500'] as const;

const levelOptions = [
  { value: 'all', label: 'all levels' },
  { value: 'error', label: 'errors' },
  { value: 'error,warn', label: 'errors + warnings' },
  { value: 'warn', label: 'warnings' },
  { value: 'info', label: 'info' },
  { value: 'debug', label: 'debug' },
] as const;

type LevelFilter = (typeof levelOptions)[number]['value'];

interface SearchParams {
  q: string;
  after: string;
  before: string;
  limit: string;
  stream: string;
  level: string;
}

export default function LogSearch({
  serviceId,
  onUnauthorized,
}: {
  serviceId: string;
  onUnauthorized: () => void;
}) {
  const [query, setQuery] = useState('');
  const [after, setAfter] = useState('');
  const [before, setBefore] = useState('');
  const [limit, setLimit] = useState<(typeof limitOptions)[number]>('100');
  const [stream, setStream] = useState<'all' | 'stdout' | 'stderr'>('all');
  const [level, setLevel] = useState<LevelFilter>('all');
  const [lines, setLines] = useState<LogLine[] | null>(null);
  const [stats, setStats] = useState<LogStats | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [context, setContext] = useState<LogContext | null>(null);
  const [contextError, setContextError] = useState<string | null>(null);
  const [contextOpen, setContextOpen] = useState(false);
  const [loadingContext, setLoadingContext] = useState(false);

  const executeSearch = useCallback(
    async (params: SearchParams) => {
      setSearching(true);
      setError(null);

      try {
        const [fetched, fetchedStats] = await Promise.all([
          fetchLogs(serviceId, {
            q: params.q,
            after: params.after,
            before: params.before,
            stream: params.stream === 'all' ? '' : params.stream,
            level: params.level === 'all' ? '' : params.level,
            limit: Number(params.limit),
          }),
          fetchLogStats(serviceId),
        ]);
        setLines(fetched);
        setStats(fetchedStats);
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setError(err instanceof Error ? err.message : String(err));
      } finally {
        setSearching(false);
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
    });
  }, [executeSearch, query, after, before, limit, stream, level]);

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
    });
  };

  // Initial load per service only — typing in the filters must not
  // refire the search (the ref holds the latest closure instead).
  const searchRef = useRef(search);
  searchRef.current = search;

  useEffect(() => {
    void searchRef.current();
  }, [serviceId]);

  const loadOlder = useCallback(async () => {
    if (lines === null || lines.length === 0) {
      return;
    }

    const oldest = lines.reduce((a, b) => (a.ts < b.ts ? a : b)).ts;
    setLoadingOlder(true);
    setError(null);

    try {
      const fetched = await fetchLogs(serviceId, {
        q: query.trim(),
        after: after.trim(),
        before: oldest,
        stream: stream === 'all' ? '' : stream,
        level: level === 'all' ? '' : level,
        limit: Number(limit),
      });
      const seen = new Set(lines.map((line) => line.id));
      const fresh = fetched.filter((line) => !seen.has(line.id));
      setLines([...lines, ...fresh]);

      if (fresh.length === 0) {
        setBefore(oldest);
      }
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoadingOlder(false);
    }
  }, [lines, serviceId, query, after, limit, stream, level, onUnauthorized]);

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

  return (
    <div className="space-y-4">
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
            <Input
              placeholder="After (RFC 3339)"
              value={after}
              onChange={(event) => setAfter(event.target.value)}
              className="lg:w-52"
              aria-label="After timestamp"
            />
            <Input
              placeholder="Before (RFC 3339)"
              value={before}
              onChange={(event) => setBefore(event.target.value)}
              className="lg:w-52"
              aria-label="Before timestamp"
            />
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
            <Select value={level} onValueChange={(value) => applyLevel(value as LevelFilter)}>
              <SelectTrigger className="w-40" aria-label="Level filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {levelOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button onClick={() => void search()} disabled={searching}>
              {searching ? 'Searching…' : 'Search'}
            </Button>
            {stats !== null && (
              <p className="text-muted-foreground ml-auto text-xs">
                {stats.lines.toLocaleString()} stored
                {stats.drops > 0 && ` · ${stats.drops} dropped`}
                {stats.truncations > 0 && ` · ${stats.truncations} truncated`}
              </p>
            )}
          </div>
        </CardContent>
      </Card>

      {error !== null && (
        <ErrorState title="Couldn't search logs" message={error} onRetry={() => void search()} />
      )}

      {lines === null && error === null && (
        <div className="space-y-2" aria-label="Loading log lines">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      )}

      {lines !== null && (
        <Card>
          <CardContent className="px-0 py-2">
            {lines.length === 0 ? (
              <p className="text-muted-foreground px-4 py-6 text-center text-sm">
                No lines match. Loosen the filters or widen the window.
              </p>
            ) : (
              <ul className="divide-y divide-border">
                {lines.map((line) => (
                  <LogLineRow key={line.id} line={line} anchor={false} onSelect={openContext} />
                ))}
              </ul>
            )}
            {lines.length > 0 && (
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
