import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { MigrationRun, MigrationStatus } from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';
import { lastSeed, runKindLabels } from '@/lib/migrations.ts';

function runVariant(state: MigrationRun['state']): 'default' | 'secondary' | 'destructive' {
  if (state === 'succeeded') {
    return 'default';
  }

  return state === 'failed' ? 'destructive' : 'secondary';
}

// SeedCard says what a seed would run and when one last ran. A seed
// leaves no record in the application's database, so the only history
// is the runs started from here.
export function SeedCard({
  seed,
  runs,
  canManage,
  busy,
  onSeed,
}: {
  seed: MigrationStatus['seed'];
  runs: MigrationRun[];
  canManage: boolean;
  busy: boolean;
  onSeed: () => void;
}) {
  const last = lastSeed(runs);

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <CardTitle className="text-base">Seed</CardTitle>
        {canManage && (
          <Button variant="outline" size="sm" onClick={onSeed} disabled={busy || !seed.configured}>
            Run seed
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {seed.configured ? (
          <p>
            The running build declares <span className="font-mono text-xs">{seed.command}</span>
          </p>
        ) : (
          <p className="text-muted-foreground">The running build declares no seed.</p>
        )}
        {last === null ? (
          <p className="text-muted-foreground">No seed has been run from touchgrass.</p>
        ) : (
          <p>
            Last run from touchgrass: <Badge variant={runVariant(last.state)}>{last.state}</Badge> by {last.actor},{' '}
            {formatTime(last.started_at)}
          </p>
        )}
        <p className="text-muted-foreground text-xs">
          Only seeds run from here are recorded. A seed run from a terminal or a deploy script does not appear.
        </p>
      </CardContent>
    </Card>
  );
}

// MigrationRunsCard lists the commands run from touchgrass. Output is
// behind a button only an admin gets: it can hold whatever a seed
// script printed.
export default function MigrationRunsCard({
  runs,
  canManage,
  onOutput,
}: {
  runs: MigrationRun[];
  canManage: boolean;
  onOutput: (run: MigrationRun) => void;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Commands run from touchgrass ({runs.length})</CardTitle>
      </CardHeader>
      <CardContent>
        {runs.length === 0 ? (
          <p className="text-muted-foreground text-sm">No migrate or seed command has been run from touchgrass.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Command</TableHead>
                <TableHead>By</TableHead>
                <TableHead>Result</TableHead>
                {canManage && <TableHead className="text-right">Output</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {runs.map((run) => (
                <TableRow key={run.id}>
                  <TableCell className="align-top text-xs whitespace-nowrap">{formatTime(run.started_at)}</TableCell>
                  <TableCell className="align-top whitespace-normal">
                    <p className="text-sm">{runKindLabels[run.kind]}</p>
                    <p className="text-muted-foreground font-mono text-xs break-all">{run.detail}</p>
                    <p className="text-muted-foreground text-xs break-all">
                      in {run.container} ({run.image})
                    </p>
                  </TableCell>
                  <TableCell className="align-top text-sm">{run.actor}</TableCell>
                  <TableCell className="align-top">
                    <Badge variant={runVariant(run.state)}>{run.state}</Badge>
                    {run.exit_code !== undefined && run.exit_code !== 0 && (
                      <span className="text-muted-foreground ml-2 text-xs">exit {run.exit_code}</span>
                    )}
                  </TableCell>
                  {canManage && (
                    <TableCell className="text-right align-top">
                      <Button variant="ghost" size="sm" onClick={() => onOutput(run)}>
                        View
                      </Button>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
