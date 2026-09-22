import { useCallback, useEffect, useState } from 'react';

import { Sheet, SheetContent, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import ErrorState from '../ErrorState.tsx';
import IssueDetail from '../issues/IssueDetail.tsx';
import IssueList from '../issues/IssueList.tsx';
import { fetchIssues, isUnauthorized, type Issue } from '@/lib/api.ts';

export default function IssuesTab({
  serviceId,
  onUnauthorized,
}: {
  serviceId: string;
  onUnauthorized: () => void;
}) {
  const [issues, setIssues] = useState<Issue[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      setIssues(await fetchIssues(serviceId));
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

  if (error !== null) {
    return <ErrorState title="Couldn't load issues" message={error} onRetry={() => void load()} />;
  }

  return (
    <>
      <IssueList
        issues={issues}
        loading={loading}
        selectedId={selectedId}
        showService={false}
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
    </>
  );
}
