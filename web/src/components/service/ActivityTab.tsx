import { useCallback, useEffect, useMemo, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from '../ErrorState.tsx';
import IssueDetail from '../issues/IssueDetail.tsx';
import LogLineRow from '../logs/LogLineRow.tsx';
import {
  fetchDeploys,
  fetchIssues,
  fetchLogs,
  fetchOccurrences,
  isUnauthorized,
  type Deploy,
  type Issue,
  type LogLine,
  type Occurrence,
} from '@/lib/api.ts';
import { formatTime, stripAnsi } from '@/lib/format.ts';

type Filter = 'all' | 'logs' | 'errors' | 'deploys';

type Row =
  | { kind: 'log'; ts: string; line: LogLine }
  | { kind: 'error'; ts: string; occurrence: Occurrence }
  | { kind: 'deploy'; ts: string; deploy: Deploy };

const maxRows = 300;

function rowTime(row: Row): number {
  return new Date(row.ts).getTime();
}

function filterKind(filter: Filter): 'log' | 'error' | 'deploy' {
  if (filter === 'logs') {
    return 'log';
  }

  if (filter === 'errors') {
    return 'error';
  }

  return 'deploy';
}

export default function ActivityTab({
  serviceId,
  onUnauthorized,
}: {
  serviceId: string;
  onUnauthorized: () => void;
}) {
  const [logs, setLogs] = useState<LogLine[] | null>(null);
  const [occurrences, setOccurrences] = useState<Occurrence[] | null>(null);
  const [deploys, setDeploys] = useState<Deploy[] | null>(null);
  const [issues, setIssues] = useState<Issue[]>([]);
  const [filter, setFilter] = useState<Filter>('all');
  const [selectedIssueId, setSelectedIssueId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      const [fetchedLogs, fetchedOccurrences, fetchedDeploys, fetchedIssues] = await Promise.all([
        fetchLogs(serviceId, { limit: 200 }),
        fetchOccurrences(serviceId, 100),
        fetchDeploys(serviceId),
        fetchIssues(serviceId),
      ]);
      setLogs(fetchedLogs);
      setOccurrences(fetchedOccurrences);
      setDeploys(fetchedDeploys.slice(0, 10));
      setIssues(fetchedIssues);
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

  const titles = useMemo(() => {
    const map = new Map<number, string>();

    for (const issue of issues) {
      map.set(issue.id, issue.title);
    }

    return map;
  }, [issues]);

  const rows = useMemo(() => {
    const merged: Row[] = [];

    for (const line of logs ?? []) {
      merged.push({ kind: 'log', ts: line.ts, line });
    }

    for (const occurrence of occurrences ?? []) {
      merged.push({ kind: 'error', ts: occurrence.created_at, occurrence });
    }

    for (const deploy of deploys ?? []) {
      merged.push({ kind: 'deploy', ts: deploy.started_at ?? deploy.created_at, deploy });
    }

    merged.sort((a, b) => rowTime(b) - rowTime(a));

    return merged.slice(0, maxRows);
  }, [logs, occurrences, deploys]);

  const counts = useMemo(() => {
    const counts = { all: rows.length, logs: 0, errors: 0, deploys: 0 };

    for (const row of rows) {
      if (row.kind === 'log') {
        counts.logs += 1;
      } else if (row.kind === 'error') {
        counts.errors += 1;
      } else {
        counts.deploys += 1;
      }
    }

    return counts;
  }, [rows]);

  const visible =
    filter === 'all' ? rows : rows.filter((row) => row.kind === filterKind(filter));

  if (error !== null) {
    return <ErrorState title="Couldn't load activity" message={error} onRetry={() => void load()} />;
  }

  const filters: { value: Filter; label: string }[] = [
    { value: 'all', label: `All (${counts.all})` },
    { value: 'logs', label: `Logs (${counts.logs})` },
    { value: 'errors', label: `Errors (${counts.errors})` },
    { value: 'deploys', label: `Deploys (${counts.deploys})` },
  ];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {filters.map((option) => (
          <Button
            key={option.value}
            variant={filter === option.value ? 'default' : 'outline'}
            size="sm"
            onClick={() => setFilter(option.value)}
          >
            {option.label}
          </Button>
        ))}
        <div className="flex-1" />
        <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
          {loading ? 'Loading…' : 'Refresh'}
        </Button>
      </div>

      {loading && logs === null ? (
        <div className="space-y-1" aria-label="Loading activity">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      ) : visible.length === 0 ? (
        <p className="text-muted-foreground px-4 py-6 text-center text-sm">
          Nothing here yet — activity appears as the service logs, errors, and deploys.
        </p>
      ) : (
        <ul className="divide-y divide-border rounded-md border">
          {visible.map((row) =>
            row.kind === 'log' ? (
              <LogLineRow key={`log-${row.line.id}`} line={row.line} anchor={false} />
            ) : row.kind === 'error' ? (
              <li key={`err-${row.occurrence.id}`}>
                <button
                  type="button"
                  onClick={() =>
                    row.occurrence.issue_id !== null && setSelectedIssueId(row.occurrence.issue_id)
                  }
                  title={
                    row.occurrence.issue_id !== null
                      ? `Open issue #${row.occurrence.issue_id}`
                      : 'Not grouped into an issue yet'
                  }
                  className="hover:bg-muted/60 flex w-full items-start gap-2 px-3 py-1.5 text-left font-mono text-xs"
                >
                  <span className="shrink-0 text-[11px]">{formatTime(row.ts)}</span>
                  <Badge
                    variant={row.occurrence.type === 'exception' ? 'destructive' : 'outline'}
                    className="shrink-0 text-[10px] uppercase"
                  >
                    {row.occurrence.type === 'exception' ? 'error' : 'message'}
                  </Badge>
                  <span className="min-w-0 flex-1 break-words whitespace-pre-wrap">
                    {stripAnsi(row.occurrence.message)}
                  </span>
                  {row.occurrence.issue_id !== null && (
                    <span className="text-muted-foreground shrink-0 text-[11px]">
                      #{row.occurrence.issue_id}
                      {titles.has(row.occurrence.issue_id)
                        ? ` · ${titles.get(row.occurrence.issue_id)}`
                        : ''}
                    </span>
                  )}
                </button>
              </li>
            ) : (
              <li
                key={`deploy-${row.deploy.id}`}
                className="flex items-start gap-2 px-3 py-1.5 font-mono text-xs"
              >
                <span className="shrink-0 text-[11px]">{formatTime(row.ts)}</span>
                <Badge
                  variant={row.deploy.outcome === 'success' ? 'default' : 'destructive'}
                  className="shrink-0 text-[10px] uppercase"
                >
                  deploy
                </Badge>
                <span className="min-w-0 flex-1 break-words whitespace-pre-wrap">
                  {row.deploy.type} {row.deploy.sha.slice(0, 12)} by {row.deploy.actor} —{' '}
                  {row.deploy.outcome}
                  {row.deploy.downtime_secs !== null && ` · ${row.deploy.downtime_secs}s downtime`}
                </span>
              </li>
            ),
          )}
        </ul>
      )}

      <Sheet open={selectedIssueId !== null} onOpenChange={(open) => !open && setSelectedIssueId(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-xl">
          <SheetHeader>
            <SheetTitle>Issue #{selectedIssueId}</SheetTitle>
          </SheetHeader>
          {selectedIssueId !== null && (
            <IssueDetail issueId={selectedIssueId} onUnauthorized={onUnauthorized} />
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
