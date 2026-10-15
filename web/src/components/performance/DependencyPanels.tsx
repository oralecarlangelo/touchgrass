import { Database, MemoryStick } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import type { PostgresPerformanceRow, RedisPerformanceRow } from '@/lib/api.ts';

const labels: Record<string, string> = {
  connections_total: 'Connections',
  max_connections: 'Connection limit',
  xact_commit: 'Commits',
  xact_rollback: 'Rollbacks',
  deadlocks: 'Deadlocks',
  blks_hit: 'Cache hits',
  blks_read: 'Disk reads',
  used_memory: 'Memory used',
  maxmemory: 'Memory limit',
  connected_clients: 'Connected clients',
  blocked_clients: 'Blocked clients',
  keyspace_hits: 'Key hits',
  keyspace_misses: 'Key misses',
  evicted_keys: 'Evictions',
  expired_keys: 'Expirations',
};

function latestMetrics<T extends PostgresPerformanceRow>(rows: T[]): T[] {
  const latest = new Map<string, T>();

  for (const row of rows) {
    const key = `${row.metric}:${'command_family' in row ? (row.command_family ?? '') : ''}`;
    const current = latest.get(key);

    if (current === undefined || row.bucket_start > current.bucket_start) {
      latest.set(key, row);
    }
  }

  return [...latest.values()].sort((left, right) => left.metric.localeCompare(right.metric));
}

function MetricRows({ rows }: { rows: PostgresPerformanceRow[] }) {
  if (rows.length === 0) {
    return (
      <p className="text-muted-foreground rounded-md border border-dashed p-5 text-center text-sm">
        No samples in this range.
      </p>
    );
  }

  return (
    <div className="divide-y">
      {rows.slice(0, 8).map((row) => (
        <div key={`${row.metric}:${row.bucket_start}`} className="flex items-center justify-between gap-3 py-2 text-sm">
          <div>
            <p className="font-medium">{labels[row.metric] ?? row.metric.replace(/_/g, ' ')}</p>
            {row.reset && <p className="text-muted-foreground text-xs">Counter baseline reset</p>}
          </div>
          <div className="text-right">
            <p className="font-mono text-xs">{row.value.toLocaleString()}</p>
            {row.delta !== 0 && (
              <p className="text-muted-foreground font-mono text-xs">
                Δ {row.delta.toLocaleString()}
              </p>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

export default function DependencyPanels({
  postgres,
  redis,
}: {
  postgres: PostgresPerformanceRow[];
  redis: RedisPerformanceRow[];
}) {
  const latestPostgres = latestMetrics(postgres);
  const latestRedis = latestMetrics(redis);

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2">
              <Database className="h-4 w-4" aria-hidden />
              PostgreSQL
            </CardTitle>
            <Badge variant={latestPostgres.length === 0 ? 'outline' : 'secondary'}>
              {latestPostgres.length === 0 ? 'no signal' : 'native metrics'}
            </Badge>
          </div>
          <CardDescription>Connection, transaction, cache, and storage pressure.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <MetricRows rows={latestPostgres} />
          <p className="text-muted-foreground text-xs">
            Statement fingerprints appear after pg_stat_statements is enabled during a maintenance window.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex items-center justify-between gap-2">
            <CardTitle className="flex items-center gap-2">
              <MemoryStick className="h-4 w-4" aria-hidden />
              Redis
            </CardTitle>
            <Badge variant={latestRedis.length === 0 ? 'outline' : 'secondary'}>
              {latestRedis.length === 0 ? 'no signal' : 'native metrics'}
            </Badge>
          </div>
          <CardDescription>Memory, clients, cache outcomes, and evictions.</CardDescription>
        </CardHeader>
        <CardContent>
          <MetricRows rows={latestRedis} />
        </CardContent>
      </Card>
    </div>
  );
}
