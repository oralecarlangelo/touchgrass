import { useCallback, useEffect, useState } from 'react';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import ErrorState from './ErrorState.tsx';
import IssueDetail from './issues/IssueDetail.tsx';
import IssueList from './issues/IssueList.tsx';
import { fetchIssues, isUnauthorized, type Issue, type ServiceView } from '@/lib/api.ts';

export default function IssuesScreen({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState('all');
  const [issues, setIssues] = useState<Issue[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      if (serviceId === 'all') {
        const perService = await Promise.all(
          services.map((service) => fetchIssues(service.id)),
        );
        setIssues(
          perService
            .flat()
            .sort((a, b) => (a.last_seen < b.last_seen ? 1 : -1)),
        );
      } else {
        setIssues(await fetchIssues(serviceId));
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
  }, [serviceId, services, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error !== null) {
    return <ErrorState title="Couldn't load issues" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Issues</h1>
        <Select value={serviceId} onValueChange={setServiceId}>
          <SelectTrigger className="ml-auto w-48" aria-label="Service filter">
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
      </div>

      <IssueList
        issues={issues}
        loading={loading}
        selectedId={selectedId}
        showService={serviceId === 'all'}
        onSelect={setSelectedId}
      />

      <Sheet open={selectedId !== null} onOpenChange={(open) => !open && setSelectedId(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-xl">
          <SheetHeader>
            <SheetTitle>Issue #{selectedId}</SheetTitle>
          </SheetHeader>
          {selectedId !== null && (
            <IssueDetail issueId={selectedId} onUnauthorized={onUnauthorized} />
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
