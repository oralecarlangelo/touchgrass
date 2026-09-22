import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import CutoverPanel from '../CutoverPanel.tsx';
import DeployPanel from '../DeployPanel.tsx';
import ErrorState from '../ErrorState.tsx';
import RollbackPanel from '../RollbackPanel.tsx';
import {
  fetchDeploys,
  isUnauthorized,
  recordDeploy,
  type Deploy,
  type ServiceView,
} from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';

const downtimeConfig = {
  downtime: { label: 'Downtime (s)', color: 'var(--chart-2)' },
} satisfies ChartConfig;

const historyPageSize = 10;

function formatDowntime(secs: number): string {
  if (secs < 1) {
    return `${Math.round(secs * 1000)}ms`;
  }

  return `${secs.toFixed(1)}s`;
}

export default function DeploysTab({
  service,
  onServiceChanged,
  onUnauthorized,
}: {
  service: ServiceView;
  onServiceChanged: () => void;
  onUnauthorized: () => void;
}) {
  const [deploys, setDeploys] = useState<Deploy[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sha, setSha] = useState('');
  const [actor, setActor] = useState('');
  const [outcome, setOutcome] = useState('success');
  const [notes, setNotes] = useState('');
  const [recording, setRecording] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<Deploy | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      setDeploys(await fetchDeploys(service.id));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [service.id, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleOperationFinished = useCallback(() => {
    void load();
    onServiceChanged();
  }, [load, onServiceChanged]);

  async function handleRecordDeploy(): Promise<void> {
    if (sha.trim() === '' || actor.trim() === '') {
      setFormError('SHA and actor are required.');
      return;
    }

    setFormError(null);
    setRecording(true);

    try {
      await recordDeploy(service.id, {
        sha: sha.trim(),
        actor: actor.trim(),
        outcome,
        notes: notes.trim(),
      });
      setSha('');
      setActor('');
      setNotes('');
      toast.success('Deploy recorded.');
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setRecording(false);
    }
  }

  const trend = useMemo(
    () =>
      (deploys ?? [])
        .filter((deploy) => deploy.downtime_secs !== null)
        .slice(0, 20)
        .reverse()
        .map((deploy) => ({
          label: `#${deploy.id}`,
          downtime: deploy.downtime_secs ?? 0,
        })),
    [deploys],
  );

  const pageCount = Math.max(1, Math.ceil((deploys ?? []).length / historyPageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = (deploys ?? []).slice(
    safePage * historyPageSize,
    safePage * historyPageSize + historyPageSize,
  );

  if (error !== null) {
    return <ErrorState title="Couldn't load deploy history" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      {service.strategy === 'bluegreen' && (
        <CutoverPanel service={service} onFinished={handleOperationFinished} />
      )}

      {service.strategy === 'recreate' && (
        <DeployPanel service={service} onFinished={handleOperationFinished} />
      )}

      {(service.strategy === 'bluegreen' || service.strategy === 'recreate') && (
        <RollbackPanel service={service} onFinished={handleOperationFinished} />
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">Downtime per deploy, oldest → newest</CardTitle>
        </CardHeader>
        <CardContent>
          {deploys === null ? (
            <Skeleton className="h-40 w-full" />
          ) : trend.length === 0 ? (
            <p className="text-muted-foreground text-sm">
              No probed deploys yet — downtime lands here after the first run.
            </p>
          ) : (
            <ChartContainer config={downtimeConfig} className="h-40 w-full">
              <BarChart data={trend}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="label" tickLine={false} axisLine={false} />
                <YAxis tickLine={false} axisLine={false} width={40} />
                <ChartTooltip content={<ChartTooltipContent />} />
                <Bar dataKey="downtime" fill="var(--color-downtime)" radius={2} />
              </BarChart>
            </ChartContainer>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">History</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {deploys === null ? (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : deploys.length === 0 ? (
            <p className="text-muted-foreground text-sm">Nothing recorded yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>SHA</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Outcome</TableHead>
                  <TableHead>Downtime</TableHead>
                  <TableHead>When</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((deploy) => (
                  <TableRow
                    key={deploy.id}
                    onClick={() => setSelected(deploy)}
                    className="cursor-pointer"
                  >
                    <TableCell className="font-mono" title={deploy.notes === '' ? undefined : deploy.notes}>
                      {deploy.sha.slice(0, 12)}
                    </TableCell>
                    <TableCell>{deploy.actor}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{deploy.type}</Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={deploy.outcome === 'success' ? 'default' : 'destructive'}>
                        {deploy.outcome}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      {deploy.downtime_secs === null ? '—' : formatDowntime(deploy.downtime_secs)}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {formatTime(deploy.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          {(deploys ?? []).length > historyPageSize && (
            <div className="flex items-center justify-between text-sm">
              <p className="text-muted-foreground">{(deploys ?? []).length} deploys</p>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={safePage === 0}
                  onClick={() => setPage(safePage - 1)}
                >
                  Previous
                </Button>
                <span className="text-muted-foreground">
                  {safePage + 1} / {pageCount}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={safePage + 1 >= pageCount}
                  onClick={() => setPage(safePage + 1)}
                >
                  Next
                </Button>
              </div>
            </div>
          )}

          {formError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {formError}
            </p>
          )}

          <div className="flex flex-wrap items-end gap-2 border-t pt-4">
            <div className="space-y-1">
              <Label htmlFor={`sha-${service.id}`}>Image SHA</Label>
              <Input
                id={`sha-${service.id}`}
                value={sha}
                onChange={(event) => setSha(event.target.value)}
                className="w-36 font-mono"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor={`actor-${service.id}`}>Actor</Label>
              <Input
                id={`actor-${service.id}`}
                value={actor}
                onChange={(event) => setActor(event.target.value)}
                className="w-32"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor={`outcome-${service.id}`}>Outcome</Label>
              <Select value={outcome} onValueChange={setOutcome}>
                <SelectTrigger id={`outcome-${service.id}`} className="w-32">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="success">success</SelectItem>
                  <SelectItem value="failure">failure</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="min-w-44 flex-1 space-y-1">
              <Label htmlFor={`notes-${service.id}`}>Notes (optional)</Label>
              <Input
                id={`notes-${service.id}`}
                value={notes}
                onChange={(event) => setNotes(event.target.value)}
              />
            </div>
            <Button onClick={() => void handleRecordDeploy()} disabled={recording}>
              {recording ? 'Recording…' : 'Record deploy'}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-md">
          <SheetHeader>
            <SheetTitle>Deploy #{selected?.id}</SheetTitle>
            <SheetDescription>Full record for this deploy.</SheetDescription>
          </SheetHeader>
          {selected !== null && (
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">SHA</dt>
                <dd className="font-mono text-xs break-all">{selected.sha}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Actor</dt>
                <dd>{selected.actor}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Type</dt>
                <dd>
                  <Badge variant="outline">{selected.type}</Badge>
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Outcome</dt>
                <dd>
                  <Badge variant={selected.outcome === 'success' ? 'default' : 'destructive'}>
                    {selected.outcome}
                  </Badge>
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Started</dt>
                <dd className="text-xs">
                  {selected.started_at === null ? '—' : formatTime(selected.started_at)}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Finished</dt>
                <dd className="text-xs">
                  {selected.finished_at === null ? '—' : formatTime(selected.finished_at)}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Duration</dt>
                <dd className="font-mono text-xs">
                  {selected.duration_secs === null ? '—' : `${selected.duration_secs}s`}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Downtime</dt>
                <dd className="font-mono text-xs">
                  {selected.downtime_secs === null ? '—' : formatDowntime(selected.downtime_secs)}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted-foreground">Recorded</dt>
                <dd className="text-xs">{formatTime(selected.created_at)}</dd>
              </div>
              {selected.notes !== '' && (
                <div className="space-y-1">
                  <dt className="text-muted-foreground">Notes</dt>
                  <dd className="rounded-md border p-2 text-xs break-words">{selected.notes}</dd>
                </div>
              )}
            </dl>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}
