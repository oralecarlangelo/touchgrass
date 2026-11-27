import { useState } from 'react';
import { ArrowRight, Lock, RefreshCw } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import EmptyState from './EmptyState.tsx';
import ErrorState from './ErrorState.tsx';
import { fetchEnvFleet, type EnvFleetSummary, type ServiceView } from '@/lib/api.ts';
import { envDocsHref } from '@/lib/env.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

export interface EnvFleetRow {
  service: ServiceView;
  overview: EnvFleetSummary | null;
  error: string | null;
}

export function envRowSummary(row: EnvFleetRow): {
  label: string;
  tone: 'muted' | 'ok' | 'pending' | 'running' | 'error';
} {
  if (row.error !== null) {
    return { label: row.error, tone: 'error' };
  }
  if (row.overview === null) {
    return { label: 'Loading…', tone: 'muted' };
  }
  if (!row.overview.configured) {
    return {
      label: row.overview.reason === 'key_missing' ? 'No encryption key on the host' :
        row.overview.reason === 'invalid_target' ? 'Invalid env target configuration' : 'No env target configured',
      tone: 'muted',
    };
  }
  if (row.overview.apply_in_progress) {
    return { label: 'Apply running', tone: 'running' };
  }
  if (row.overview.pending_count > 0) {
    return {
      label: `${row.overview.pending_count} ${row.overview.pending_count === 1 ? 'change' : 'changes'} not applied`,
      tone: 'pending',
    };
  }
  if (row.overview.key_count === 0) {
    return { label: 'No entries yet', tone: 'muted' };
  }

  return { label: 'Up to date', tone: 'ok' };
}

const toneClass = {
  muted: 'text-muted-foreground',
  ok: 'text-emerald-700 dark:text-emerald-300',
  pending: 'text-amber-700 dark:text-amber-300',
  running: 'text-foreground',
  error: 'text-destructive',
} as const;

// EnvironmentScreenView lists every managed service with its environment
// status; the per-service work happens on the service's Environment tab.
export function EnvironmentScreenView({
  rows,
  loading,
  isAdmin,
  onRefresh,
  onOpenService,
}: {
  rows: EnvFleetRow[] | null;
  loading: boolean;
  isAdmin: boolean;
  onRefresh: () => void;
  onOpenService: (id: string) => void;
}) {
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Environment</h1>
          <p className="text-muted-foreground text-sm">
            Each service's environment file, staged changes and apply history.{' '}
            <a href={envDocsHref} target="_blank" rel="noreferrer" className="text-primary underline-offset-4 hover:underline">
              How it works
            </a>
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={onRefresh} disabled={loading} aria-label="Refresh environment status">
          <RefreshCw className={loading ? 'animate-spin' : ''} aria-hidden />
          Refresh
        </Button>
      </div>

      {rows === null ? (
        <div className="space-y-2" aria-label="Loading environment status">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : rows.length === 0 ? (
        <EmptyState
          title="No services yet"
          body="Add a service to the inventory; its environment file shows up here."
        />
      ) : (
        <Card className="py-0">
          <CardContent className="divide-border divide-y px-0">
            {rows.map((row) => {
              const summary = envRowSummary(row);
              const overview = row.overview;
              const secrets = overview?.secret_count ?? 0;

              return (
                <div key={row.service.id} className="flex flex-wrap items-center gap-3 px-4 py-3" data-service={row.service.id}>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">{row.service.name}</p>
                    <p className="text-muted-foreground truncate font-mono text-xs">
                      {overview?.target_path ?? row.service.id}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2 text-xs">
                    {overview !== null && overview.configured && overview.key_count > 0 && (
                      <Badge variant="outline">
                        {overview.key_count} {overview.key_count === 1 ? 'key' : 'keys'}
                        {secrets > 0 && (
                          <>
                            <Lock className="size-3" aria-hidden /> {secrets}
                          </>
                        )}
                      </Badge>
                    )}
                    <span className={toneClass[summary.tone]}>{summary.label}</span>
                    {overview?.last_applied_at != null && (
                      <span className="text-muted-foreground" title={formatTime(overview.last_applied_at)}>
                        applied {timeAgo(overview.last_applied_at) || formatTime(overview.last_applied_at)}
                      </span>
                    )}
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    aria-label={`Open environment for ${row.service.name}`}
                    onClick={() => onOpenService(row.service.id)}
                  >
                    {isAdmin ? 'Manage' : 'View'}
                    <ArrowRight aria-hidden />
                  </Button>
                </div>
              );
            })}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

export default function EnvironmentScreen({
  services,
  isAdmin,
  onUnauthorized,
  onOpenService,
}: {
  services: ServiceView[] | null;
  isAdmin: boolean;
  onUnauthorized: () => void;
  onOpenService: (id: string) => void;
}) {
  const [refresh, setRefresh] = useState(0);
  const scope = JSON.stringify(services?.map(({ id }) => id) ?? []);
  const fleet = usePanelRead(scope, services?.length ? fetchEnvFleet : null, refresh, onUnauthorized);
  const summary = new Map(fleet.data?.map((row) => [row.service_id, row]) ?? []);
  const rows = services?.map((service) => ({
    service,
    overview: summary.get(service.id) ?? null,
    error: fleet.data !== null && !summary.has(service.id) ? 'Status unavailable' : null,
  })) ?? null;

  return (
    <>
      {fleet.error !== null && (
        <ErrorState title="Couldn’t refresh environment status" message={fleet.error} onRetry={fleet.retry} />
      )}
      <EnvironmentScreenView
        rows={rows}
        loading={fleet.loading || services === null}
        isAdmin={isAdmin}
        onRefresh={() => setRefresh((value) => value + 1)}
        onOpenService={onOpenService}
      />
    </>
  );
}
