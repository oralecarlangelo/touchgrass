import { useCallback, useMemo, useState } from 'react';

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
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from './ErrorState.tsx';
import { fetchAudit, type ServiceView } from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';
import { readInvestigation, type InvestigationRange } from '@/lib/investigation.ts';
import { useLocationHash } from '@/lib/use-location-hash.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

const pageSize = 25;

interface HistoryProps {
  services: ServiceView[];
  fixedServiceId?: string;
  onUnauthorized: () => void;
}

export default function HistoryScreen(props: HistoryProps) {
  const hash = useLocationHash('audit');
  if (props.fixedServiceId === undefined && hash === null) return null;

  return (
    <AuditHistory
      key={props.fixedServiceId ?? hash}
      {...props}
      link={props.fixedServiceId === undefined && hash !== null ? readInvestigation(hash) : {}}
    />
  );
}

function validBound(value: string | null): string | undefined {
  if (!value) return undefined;
  const time = new Date(value);
  return Number.isNaN(time.valueOf()) ? undefined : time.toISOString();
}

function AuditHistory({
  services,
  fixedServiceId,
  onUnauthorized,
  link,
}: HistoryProps & { link: InvestigationRange }) {
  const [serviceId, setServiceId] = useState(link.serviceId ?? 'all');
  const [action, setAction] = useState('');
  const [actor, setActor] = useState('');
  const [since, setSince] = useState('');
  const [showLinkRange, setShowLinkRange] = useState(true);
  const [result, setResult] = useState('all');
  const [page, setPage] = useState(0);

  const effectiveServiceId = fixedServiceId ?? (serviceId === 'all' ? '' : serviceId);
  const linkedStart = showLinkRange ? link.start : undefined;
  const end = showLinkRange ? link.end : undefined;
  const start = since ? validBound(`${since}T00:00:00`) : linkedStart;

  const readHistory = useCallback(
    (signal: AbortSignal) => fetchAudit(effectiveServiceId, { start, end }, signal),
    [effectiveServiceId, start, end],
  );
  const readScope = JSON.stringify([effectiveServiceId, start, end]);
  const { data: entries, error, retry } = usePanelRead(readScope, readHistory, 0, onUnauthorized);

  const results = useMemo(() => [...new Set((entries ?? []).map((entry) => entry.result))].sort(), [entries]);
  const filtered = useMemo(() => {
    const actionNeedle = action.trim().toLowerCase();
    const actorNeedle = actor.trim().toLowerCase();
    return (entries ?? []).filter((entry) =>
      entry.action.toLowerCase().includes(actionNeedle)
      && entry.actor.toLowerCase().includes(actorNeedle)
      && (result === 'all' || entry.result === result),
    );
  }, [entries, action, actor, result]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = filtered.slice(safePage * pageSize, safePage * pageSize + pageSize);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Audit history</h1>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Input
            placeholder="Filter action…"
            value={action}
            onChange={(event) => {
              setAction(event.target.value);
              setPage(0);
            }}
            className="w-52"
            aria-label="Filter by action"
          />
          <Input className="w-36" placeholder="Filter actor…" aria-label="Filter by actor" value={actor}
            onChange={(event) => { setActor(event.target.value); setPage(0); }} />
          <Input type="date" className="w-36" aria-label="Entries since (local date)" value={since}
            onChange={(event) => {
              setSince(event.target.value);
              setShowLinkRange(false);
              setPage(0);
            }} />
          <Select value={result} onValueChange={(value) => { setResult(value); setPage(0); }}>
            <SelectTrigger className="w-32" aria-label="Filter by result"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All results</SelectItem>
              {results.filter(Boolean).map((value) => <SelectItem key={value} value={value}>{value}</SelectItem>)}
            </SelectContent>
          </Select>
          {fixedServiceId === undefined && (
            <Select
              value={serviceId}
              onValueChange={(value) => {
                setServiceId(value);
                setPage(0);
              }}
            >
              <SelectTrigger className="w-48" aria-label="Service filter">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="all">All services</SelectItem>
                {services.map((service) => (
                  <SelectItem key={service.id} value={service.id}>
                    {service.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </div>
      </div>

      {(linkedStart || end) && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <p className="text-muted-foreground">
            Entries {linkedStart && `from ${formatTime(linkedStart)}`}{' '}
            {end && `before ${formatTime(end)}`}
          </p>
          <Button variant="ghost" size="sm" onClick={() => { setShowLinkRange(false); setPage(0); }}>
            Clear time range
          </Button>
        </div>
      )}
      {error && <ErrorState title="Couldn't load audit history" message={error} onRetry={retry} />}
      {(entries !== null || !error) && <Card>
        <CardContent className="pt-4">
          {entries === null ? (
            <div className="space-y-2" aria-label="Loading audit entries">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>When</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Service</TableHead>
                  <TableHead>Result</TableHead>
                  <TableHead>Detail</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((entry) => (
                  <TableRow key={entry.id}>
                    <TableCell className="text-muted-foreground text-xs whitespace-nowrap">
                      {formatTime(entry.created_at)}
                    </TableCell>
                    <TableCell className="font-medium">{entry.actor}</TableCell>
                    <TableCell className="font-mono text-xs">{entry.action}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {entry.service_id ?? '—'}
                    </TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          entry.result === 'ok' || entry.result === 'success'
                            ? 'default'
                            : entry.result === 'denied' || entry.result === 'error'
                              ? 'destructive'
                              : 'secondary'
                        }
                      >
                        {entry.result}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground w-64 text-xs">
                      {entry.detail === '' ? '—' : (
                        <details>
                          <summary className="cursor-pointer">View detail</summary>
                          <p className="mt-2 break-words whitespace-pre-wrap">{entry.detail}</p>
                        </details>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
                {rows.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={6} className="text-muted-foreground text-center">
                      No entries match these filters.
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>}

      <div className="flex items-center justify-between text-sm">
        <p className="text-muted-foreground">
          {filtered.length} entr{filtered.length === 1 ? 'y' : 'ies'}
        </p>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" disabled={safePage === 0} onClick={() => setPage(safePage - 1)}>
            Previous
          </Button>
          <span className="text-muted-foreground">
            {safePage + 1} / {pageCount}
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={safePage + 1 >= pageCount}
            onClick={() => setPage(safePage + 1)}
          >
            Next
          </Button>
        </div>
      </div>
    </div>
  );
}
