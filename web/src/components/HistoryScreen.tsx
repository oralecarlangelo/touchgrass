import { useCallback, useEffect, useMemo, useState } from 'react';

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
import { fetchAudit, isUnauthorized, type AuditEntry, type ServiceView } from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';

const pageSize = 25;

export default function HistoryScreen({
  services,
  fixedServiceId,
  onUnauthorized,
}: {
  services: ServiceView[];
  fixedServiceId?: string;
  onUnauthorized: () => void;
}) {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [serviceId, setServiceId] = useState('all');
  const [action, setAction] = useState('');
  const [page, setPage] = useState(0);

  const effectiveServiceId = fixedServiceId ?? (serviceId === 'all' ? '' : serviceId);

  const load = useCallback(async () => {
    setError(null);

    try {
      setEntries(await fetchAudit(effectiveServiceId));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [effectiveServiceId, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  const filtered = useMemo(() => {
    const needle = action.trim().toLowerCase();

    if (needle === '') {
      return entries ?? [];
    }

    return (entries ?? []).filter(
      (entry) =>
        entry.action.toLowerCase().includes(needle) ||
        entry.actor.toLowerCase().includes(needle),
    );
  }, [entries, action]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = filtered.slice(safePage * pageSize, safePage * pageSize + pageSize);

  if (error !== null) {
    return <ErrorState title="Couldn't load audit history" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Audit history</h1>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Input
            placeholder="Filter action or actor…"
            value={action}
            onChange={(event) => {
              setAction(event.target.value);
              setPage(0);
            }}
            className="w-52"
            aria-label="Filter audit entries"
          />
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

      <Card>
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
                    <TableCell
                      className="text-muted-foreground max-w-64 truncate text-xs"
                      title={entry.detail}
                    >
                      {entry.detail === '' ? '—' : entry.detail}
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
      </Card>

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
