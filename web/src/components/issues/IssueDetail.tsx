import { useCallback, useEffect, useMemo, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import ErrorState from '../ErrorState.tsx';
import {
  fetchIssue,
  fetchIssueLogs,
  isUnauthorized,
  type Issue,
  type LogLine,
  type Occurrence,
} from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';
import LogLineRow from '../logs/LogLineRow.tsx';

const windowOptions = [
  { value: '60', label: '±1 minute' },
  { value: '300', label: '±5 minutes' },
  { value: '900', label: '±15 minutes' },
] as const;

const occurrencesPageSize = 5;

function OccurrenceCard({ occurrence }: { occurrence: Occurrence }) {
  return (
    <div className="rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="font-mono text-[11px]">
          {occurrence.type}
        </Badge>
        {occurrence.release !== '' && (
          <Badge variant="secondary" className="font-mono text-[11px]">
            {occurrence.release}
          </Badge>
        )}
        <span className="text-muted-foreground ml-auto text-xs">
          {formatTime(occurrence.created_at)}
        </span>
      </div>
      <p className="mt-2 font-mono text-xs break-words whitespace-pre-wrap">{occurrence.message}</p>
      {occurrence.stack.length > 0 && (
        <details className="mt-2">
          <summary className="cursor-pointer text-xs font-medium">
            Stack trace ({occurrence.stack.length} frames)
          </summary>
          <ol className="mt-1 space-y-0.5 font-mono text-[11px]">
            {occurrence.stack.map((frame, index) => (
              <li key={`${frame.file}-${frame.line}-${index}`} className="break-all">
                <span className="text-muted-foreground">at </span>
                {frame.function}
                <span className="text-muted-foreground">
                  {' '}
                  ({frame.file}:{frame.line}:{frame.column})
                </span>
              </li>
            ))}
          </ol>
        </details>
      )}
      {occurrence.breadcrumbs.length > 0 && (
        <details className="mt-2">
          <summary className="cursor-pointer text-xs font-medium">
            Breadcrumbs ({occurrence.breadcrumbs.length})
          </summary>
          <ul className="mt-1 space-y-0.5 text-[11px]">
            {occurrence.breadcrumbs.map((crumb, index) => (
              <li key={`${crumb.at}-${index}`}>
                <span className="text-muted-foreground font-mono">{formatTime(crumb.at)} </span>
                <Badge variant="outline" className="mr-1 text-[10px]">
                  {crumb.category}
                </Badge>
                {crumb.message}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  );
}

export default function IssueDetail({
  issueId,
  onUnauthorized,
}: {
  issueId: number;
  onUnauthorized: () => void;
}) {
  const [issue, setIssue] = useState<Issue | null>(null);
  const [occurrences, setOccurrences] = useState<Occurrence[]>([]);
  const [logs, setLogs] = useState<LogLine[] | null>(null);
  const [windowSecs, setWindowSecs] =
    useState<(typeof windowOptions)[number]['value']>('60');
  const [error, setError] = useState<string | null>(null);
  const [logsError, setLogsError] = useState<string | null>(null);
  const [occurrencePage, setOccurrencePage] = useState(0);

  const load = useCallback(async () => {
    setError(null);

    try {
      const detail = await fetchIssue(issueId);
      setIssue(detail.issue);
      setOccurrences(detail.occurrences);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [issueId, onUnauthorized]);

  const loadLogs = useCallback(async () => {
    setLogsError(null);
    setLogs(null);

    try {
      setLogs(await fetchIssueLogs(issueId, Number(windowSecs)));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setLogsError(err instanceof Error ? err.message : String(err));
    }
  }, [issueId, windowSecs, onUnauthorized]);

  useEffect(() => {
    setOccurrencePage(0);
    void load();
  }, [load]);

  const occurrencePageCount = Math.max(
    1,
    Math.ceil(occurrences.length / occurrencesPageSize),
  );
  const safeOccurrencePage = Math.min(occurrencePage, occurrencePageCount - 1);
  const visibleOccurrences = useMemo(
    () =>
      occurrences.slice(
        safeOccurrencePage * occurrencesPageSize,
        safeOccurrencePage * occurrencesPageSize + occurrencesPageSize,
      ),
    [occurrences, safeOccurrencePage],
  );

  useEffect(() => {
    void loadLogs();
  }, [loadLogs]);

  if (error !== null) {
    return <ErrorState title="Couldn't load the issue" message={error} onRetry={() => void load()} />;
  }

  if (issue === null) {
    return (
      <div className="space-y-2" aria-label="Loading issue">
        <Skeleton className="h-6 w-2/3" />
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div>
        <h3 className="text-base font-semibold break-words">{issue.title}</h3>
        <p className="text-muted-foreground font-mono text-[11px] break-all">
          {issue.fingerprint}
        </p>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
          <span>
            <span className="text-muted-foreground">Events </span>
            <span className="font-mono font-medium">{issue.count}</span>
          </span>
          <span>
            <span className="text-muted-foreground">First seen </span>
            {formatTime(issue.first_seen)}
          </span>
          <span>
            <span className="text-muted-foreground">Last seen </span>
            {formatTime(issue.last_seen)}
          </span>
        </div>
      </div>

      <Tabs defaultValue="occurrences">
        <TabsList>
          <TabsTrigger value="occurrences">Occurrences ({occurrences.length})</TabsTrigger>
          <TabsTrigger value="logs">Logs around crash</TabsTrigger>
        </TabsList>
        <TabsContent value="occurrences" className="space-y-2">
          {occurrences.length === 0 && (
            <p className="text-muted-foreground text-sm">No occurrences retained.</p>
          )}
          {visibleOccurrences.map((occurrence) => (
            <OccurrenceCard key={occurrence.id} occurrence={occurrence} />
          ))}
          {occurrences.length > occurrencesPageSize && (
            <div className="flex items-center justify-between text-sm">
              <p className="text-muted-foreground">{occurrences.length} occurrences</p>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={safeOccurrencePage === 0}
                  onClick={() => setOccurrencePage(safeOccurrencePage - 1)}
                >
                  Previous
                </Button>
                <span className="text-muted-foreground">
                  {safeOccurrencePage + 1} / {occurrencePageCount}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={safeOccurrencePage + 1 >= occurrencePageCount}
                  onClick={() => setOccurrencePage(safeOccurrencePage + 1)}
                >
                  Next
                </Button>
              </div>
            </div>
          )}
        </TabsContent>
        <TabsContent value="logs" className="space-y-2">
          <div className="flex items-center gap-2">
            <Select value={windowSecs} onValueChange={(value) => setWindowSecs(value as typeof windowSecs)}>
              <SelectTrigger className="w-40" aria-label="Log window">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {windowOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {logsError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {logsError}
            </p>
          )}
          {logs === null && logsError === null && (
            <div className="space-y-1" aria-label="Loading issue logs">
              <Skeleton className="h-6 w-full" />
              <Skeleton className="h-6 w-full" />
            </div>
          )}
          {logs !== null && logs.length === 0 && (
            <p className="text-muted-foreground text-sm">
              No log lines in this window — widen it or check retention.
            </p>
          )}
          {logs !== null && logs.length > 0 && (
            <ul className="divide-y divide-border rounded-md border">
              {logs.map((line) => (
                <LogLineRow key={line.id} line={line} anchor={false} />
              ))}
            </ul>
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}
