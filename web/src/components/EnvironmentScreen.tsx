import { useCallback, useEffect, useState } from 'react';
import { ArrowRight, Lock, RefreshCw } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import EmptyState from './EmptyState.tsx';
import ErrorState from './ErrorState.tsx';
import { ApiError, fetchEnv, isUnauthorized, type EnvOverview, type ServiceView } from '@/lib/api.ts';
import { envDocsHref } from '@/lib/env.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

export interface EnvFleetRow {
  service: ServiceView;
  overview: EnvOverview | null;
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
      label: row.overview.reason === 'key_missing' ? 'No encryption key on the host' : 'No env target configured',
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
  if (row.overview.entries.length === 0) {
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
              const secrets = overview?.entries.filter((entry) => entry.is_secret).length ?? 0;

              return (
                <div key={row.service.id} className="flex flex-wrap items-center gap-3 px-4 py-3" data-service={row.service.id}>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium">{row.service.name}</p>
                    <p className="text-muted-foreground truncate font-mono text-xs">
                      {overview?.target?.path ?? row.service.id}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2 text-xs">
                    {overview !== null && overview.configured && overview.entries.length > 0 && (
                      <Badge variant="outline">
                        {overview.entries.length} {overview.entries.length === 1 ? 'key' : 'keys'}
                        {secrets > 0 && (
                          <>
                            <Lock className="size-3" aria-hidden /> {secrets}
                          </>
                        )}
                      </Badge>
                    )}
                    <span className={toneClass[summary.tone]}>{summary.label}</span>
                    {overview?.applied_revision != null && (
                      <span className="text-muted-foreground" title={formatTime(overview.applied_revision.created_at)}>
                        applied {timeAgo(overview.applied_revision.created_at) || formatTime(overview.applied_revision.created_at)}
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
  const [rows, setRows] = useState<EnvFleetRow[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (services === null) {
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const results = await Promise.all(
        services.map(async (service): Promise<EnvFleetRow> => {
          try {
            return { service, overview: await fetchEnv(service.id), error: null };
          } catch (err) {
            if (isUnauthorized(err)) {
              throw err;
            }
            if (err instanceof ApiError && err.status === 503) {
              return {
                service,
                overview: {
                  configured: false,
                  reason: 'key_missing',
                  target: null,
                  entries: [],
                  applied_revision: null,
                  pending_count: 0,
                  last_validation: null,
                  apply_in_progress: false,
                },
                error: null,
              };
            }

            return { service, overview: null, error: err instanceof Error ? err.message : String(err) };
          }
        }),
      );
      setRows(results);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [services, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error !== null) {
    return <ErrorState title="Couldn't load environment status" message={error} onRetry={() => void load()} />;
  }

  return (
    <EnvironmentScreenView
      rows={services === null ? null : (rows ?? services.map((service) => ({ service, overview: null, error: null })))}
      loading={loading}
      isAdmin={isAdmin}
      onRefresh={() => void load()}
      onOpenService={onOpenService}
    />
  );
}
