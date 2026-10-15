import { CircleCheck, CircleDashed, CircleX, RadioTower } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import type { CollectorStatusView } from '@/lib/api.ts';
import { timeAgo } from '@/lib/format.ts';
import {
  collectorLastSuccessAt,
  collectorState,
  type CollectorState as State,
} from '@/lib/performance.ts';

const stateLabel: Record<State, string> = {
  healthy: 'healthy',
  degraded: 'data loss',
  stale: 'stale',
  'not-configured': 'setup needed',
};

function StateIcon({ state }: { state: State }) {
  if (state === 'healthy') {
    return <CircleCheck className="h-4 w-4 text-emerald-600" aria-hidden />;
  }
  if (state === 'not-configured') {
    return <CircleDashed className="text-muted-foreground h-4 w-4" aria-hidden />;
  }
  return <CircleX className="text-destructive h-4 w-4" aria-hidden />;
}

export default function CollectorStatus({ collectors }: { collectors: CollectorStatusView[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <RadioTower className="h-4 w-4" aria-hidden />
          Collector status
        </CardTitle>
        <CardDescription>Freshness and data-loss visibility for each read-only source.</CardDescription>
      </CardHeader>
      <CardContent>
        {collectors.length === 0 ? (
          <p className="text-muted-foreground text-sm">Collector status is not available.</p>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
            {collectors.map((collector) => {
              const state = collectorState(collector);
              const lastSuccessAt = collectorLastSuccessAt(collector);

              return (
                <div key={collector.source} className="rounded-md border p-3">
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                      <StateIcon state={state} />
                      <p className="text-sm font-medium">{collector.source.replace(/_/g, ' ')}</p>
                    </div>
                    <Badge variant={state === 'healthy' ? 'secondary' : 'outline'}>
                      {stateLabel[state]}
                    </Badge>
                  </div>
                  <p className="text-muted-foreground mt-2 text-xs">
                    {lastSuccessAt === null
                      ? 'No successful sample yet. Check source setup and permissions.'
                      : `Last success ${timeAgo(lastSuccessAt)}`}
                  </p>
                  {collector.dropped_records > 0 && (
                    <p className="text-destructive mt-1 text-xs">
                      {collector.dropped_records.toLocaleString()} records dropped
                    </p>
                  )}
                  {collector.last_error !== undefined && collector.last_error !== '' && (
                    <p className="text-destructive mt-1 truncate text-xs" title={collector.last_error}>
                      {collector.last_error}
                    </p>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
