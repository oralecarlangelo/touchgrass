import { useCallback, useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import ErrorState from './ErrorState.tsx';
import IssueDetail from './issues/IssueDetail.tsx';
import IssueList from './issues/IssueList.tsx';
import { fetchIssueInbox, type ServiceView } from '@/lib/api.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

interface IssuesProps {
  services: ServiceView[];
  onUnauthorized: () => void;
}

export default function IssuesScreen(props: IssuesProps) {
  const [hash, setHash] = useState(() => window.location.hash);
  useEffect(() => {
    const followHash = () => setHash(window.location.hash);
    window.addEventListener('hashchange', followHash);
    return () => window.removeEventListener('hashchange', followHash);
  }, []);

  return <IssueInbox key={hash} initial={new URLSearchParams(hash.split('?')[1] ?? '')} {...props} />;
}

function IssueInbox({ services, onUnauthorized, initial }: IssuesProps & { initial: URLSearchParams }) {
  const [serviceId, setServiceId] = useState(initial.get('service_id') || 'all');
  const [query, setQuery] = useState('');
  const [release, setRelease] = useState('');
  const [filters, setFilters] = useState({ q: '', release: '' });
  const [cursors, setCursors] = useState(['']);
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [range, setRange] = useState({ start: initial.get('start') ?? '', end: initial.get('end') ?? '' });

  const cursor = cursors[cursors.length - 1];
  const scope = JSON.stringify([serviceId, filters, cursor, range]);
  const read = useCallback((signal: AbortSignal) => fetchIssueInbox({
    ...filters,
    ...range,
    cursor,
    service_id: serviceId === 'all' ? undefined : serviceId,
  }, signal), [cursor, filters, range, serviceId]);
  const inbox = usePanelRead(scope, read, 0, onUnauthorized);
  const nextCursor = inbox.data?.next_cursor;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Issues</h1>
        <Select value={serviceId} onValueChange={(value) => { setServiceId(value); setCursors(['']); }}>
          <SelectTrigger className="ml-auto w-48" aria-label="Service filter"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All services</SelectItem>
            {services.map((service) => <SelectItem key={service.id} value={service.id}>{service.name}</SelectItem>)}
          </SelectContent>
        </Select>
      </div>

      {(range.start || range.end) && (
        <div className="text-muted-foreground flex flex-wrap items-center gap-2 text-sm">
          <span>Filtered to the selected investigation window</span>
          <Button size="sm" variant="ghost" onClick={() => { setRange({ start: '', end: '' }); setCursors(['']); }}>
            Clear time range
          </Button>
        </div>
      )}
      <form className="flex flex-col gap-2 sm:flex-row" onSubmit={(event) => {
        event.preventDefault();
        setFilters({ q: query.trim(), release: release.trim() });
        setCursors(['']);
      }}>
        <Input value={query} onChange={(event) => setQuery(event.target.value)} maxLength={200}
          aria-label="Search issue title or fingerprint" placeholder="Search title or fingerprint…" />
        <Input value={release} onChange={(event) => setRelease(event.target.value)} maxLength={200}
          aria-label="Filter by release" placeholder="Release" className="sm:w-44" />
        <Button type="submit">Search</Button>
      </form>

      {inbox.error !== null && <ErrorState title="Couldn’t load issues" message={inbox.error} onRetry={inbox.retry} />}
      {(inbox.error === null || inbox.data !== null) && (
        <IssueList issues={inbox.data?.issues ?? []} loading={inbox.loading && inbox.data === null}
          selectedId={selectedId} showService={serviceId === 'all'} onSelect={setSelectedId} serverPaged />
      )}
      <div className="flex items-center justify-end gap-2">
        <Button size="sm" variant="outline" disabled={cursors.length <= 1 || inbox.loading}
          onClick={() => setCursors((current) => current.slice(0, -1))}>Previous</Button>
        <Button size="sm" variant="outline" disabled={!nextCursor || inbox.loading}
          onClick={() => { if (nextCursor) setCursors((current) => [...current, nextCursor]); }}>Next</Button>
      </div>

      <Sheet open={selectedId !== null} onOpenChange={(open) => !open && setSelectedId(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-xl">
          <SheetHeader><SheetTitle>Issue #{selectedId}</SheetTitle></SheetHeader>
          {selectedId !== null && <IssueDetail issueId={selectedId} onUnauthorized={onUnauthorized} />}
        </SheetContent>
      </Sheet>
    </div>
  );
}
