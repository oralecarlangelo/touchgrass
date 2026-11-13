import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { Migration } from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';
import { canResolve, formatDuration, shortChecksum, stateLabels, stateTones } from '@/lib/migrations.ts';
import { toneVariant } from './MigrationStatusCard.tsx';

function Detail({ migration }: { migration: Migration }) {
  if (migration.state === 'modified') {
    return (
      <p className="text-muted-foreground text-xs">
        Applied <span className="font-mono">{shortChecksum(migration.checksum_applied)}</span>, this build ships{' '}
        <span className="font-mono">{shortChecksum(migration.checksum_shipped)}</span>
      </p>
    );
  }

  if (migration.logs !== undefined && migration.logs !== '') {
    return (
      <pre className="bg-muted mt-1 max-h-40 overflow-auto rounded p-2 text-xs whitespace-pre-wrap">{migration.logs}</pre>
    );
  }

  return null;
}

// MigrationsTable lists every migration, newest first: the newest are
// the ones that are pending or just failed.
export default function MigrationsTable({
  migrations,
  canManage,
  busy,
  onResolve,
}: {
  migrations: Migration[];
  canManage: boolean;
  busy: boolean;
  onResolve: (migration: Migration, resolution: 'applied' | 'rolled_back') => void;
}) {
  const newestFirst = [...migrations].reverse();

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">History ({migrations.length})</CardTitle>
      </CardHeader>
      <CardContent>
        {migrations.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No migrations: the database records none and the running build ships none.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Migration</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Applied</TableHead>
                <TableHead>Took</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {newestFirst.map((migration) => (
                <TableRow key={migration.name}>
                  <TableCell className="align-top whitespace-normal">
                    <p className="font-mono text-xs break-all">{migration.name}</p>
                    <Detail migration={migration} />
                    {migration.attempts > 1 && (
                      <p className="text-muted-foreground text-xs">{migration.attempts} attempts</p>
                    )}
                    {/* The resolve buttons sit under the failure they answer, so the
                        table keeps four columns and fits a narrow window. */}
                    {canManage && canResolve(migration) && (
                      <div className="mt-2 flex flex-wrap gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={busy}
                          onClick={() => onResolve(migration, 'rolled_back')}
                        >
                          Mark rolled back
                        </Button>
                        <Button variant="outline" size="sm" disabled={busy} onClick={() => onResolve(migration, 'applied')}>
                          Mark applied
                        </Button>
                      </div>
                    )}
                  </TableCell>
                  <TableCell className="align-top">
                    <Badge variant={toneVariant(stateTones[migration.state])}>{stateLabels[migration.state]}</Badge>
                  </TableCell>
                  <TableCell className="align-top text-xs whitespace-nowrap">
                    {migration.finished_at !== undefined
                      ? formatTime(migration.finished_at)
                      : migration.started_at !== undefined
                        ? `started ${formatTime(migration.started_at)}`
                        : ''}
                  </TableCell>
                  <TableCell className="align-top text-xs whitespace-nowrap">
                    {formatDuration(migration.duration_ms)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
