import { CircleCheck, CircleDashed, CircleX, Copy, RadioTower } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import type { CollectorStatusView, DatabaseView, ServiceView } from '@/lib/api.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';
import {
  collectorGuidance,
  collectorLastSuccessAt,
  collectorSetupSnippet,
  collectorState,
  type CollectorState as State,
} from '@/lib/performance.ts';

const stateLabel: Record<State, string> = {
  healthy: 'healthy',
  degraded: 'degraded',
  stale: 'stale',
  'not-configured': 'setup needed',
  disabled: 'disabled',
};

function StateIcon({ state }: { state: State }) {
  if (state === 'healthy') {
    return <CircleCheck className="h-4 w-4 text-emerald-600" aria-hidden />;
  }
  if (state === 'not-configured' || state === 'disabled') {
    return <CircleDashed className="text-muted-foreground h-4 w-4" aria-hidden />;
  }
  return <CircleX className="text-destructive h-4 w-4" aria-hidden />;
}

export default function CollectorStatus({
  collectors,
  services,
  serviceId,
  databaseSources,
  setupOnly = false,
}: {
  collectors: CollectorStatusView[];
  services: ServiceView[];
  serviceId: string;
  databaseSources?: DatabaseView[];
  setupOnly?: boolean;
}) {
  const setupSnippet = collectorSetupSnippet(collectors, services, serviceId, databaseSources);

  if (setupOnly && setupSnippet === '') {
    return <p className="text-muted-foreground text-sm">No setup suggestion for this service. Check collector status and the Databases inventory.</p>;
  }

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
        {setupSnippet !== '' && (
          <div className="bg-muted/40 mb-3 rounded-md border p-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <p className="text-sm font-medium">Connect discovered sources</p>
                <p className="text-muted-foreground mt-1 text-xs">
                  Review this inferred configuration, add it to the server environment, then restart Touch Grass.
                </p>
              </div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => void navigator.clipboard.writeText(setupSnippet)}
              >
                <Copy aria-hidden />
                Copy config
              </Button>
            </div>
            <pre className="mt-3 overflow-x-auto text-xs"><code>{setupSnippet}</code></pre>
          </div>
        )}
        {!setupOnly && (collectors.length === 0 ? (
          <p className="text-muted-foreground text-sm">Collector status is not available.</p>
        ) : (
          <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
            {collectors.map((collector) => {
              const state = collectorState(collector);
              const lastSuccessAt = collectorLastSuccessAt(collector);
              const showIncident = state === 'degraded' || state === 'stale';
              const guidance = collectorGuidance(collector) ?? (showIncident
                ? 'Check the configured source and its permissions, then look for a new successful sample.' : null);

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
                    {lastSuccessAt !== null && (
                      <>Last success <time dateTime={lastSuccessAt} title={formatTime(lastSuccessAt)}>{timeAgo(lastSuccessAt)}</time></>
                    )}
                    {lastSuccessAt === null && state !== 'disabled' && 'No successful sample yet.'}
                  </p>
                  {collector.dropped_records > 0 && (
                    <p className="text-muted-foreground mt-1 text-xs">
                      {collector.dropped_records.toLocaleString()} records dropped in collector history
                    </p>
                  )}
                  {showIncident && collector.last_error && (
                    <p className="text-destructive mt-1 break-words text-xs" title={collector.last_error}>
                      {collector.last_error.replace(/_/g, ' ')}
                    </p>
                  )}
                  {showIncident && !collector.coverage_complete && (
                    <p className="text-destructive mt-1 text-xs">Partial coverage. Prior findings remain active.</p>
                  )}
                  {guidance !== null && <p className="text-muted-foreground mt-1 text-xs">{guidance}</p>}
                  {collector.effective_interval_secs !== undefined && collector.effective_interval_secs > 0 && (
                    <p className="text-muted-foreground mt-1 text-xs">Effective interval {collector.effective_interval_secs}s</p>
                  )}
                </div>
              );
            })}
          </div>
        ))}
      </CardContent>
    </Card>
  );
}
