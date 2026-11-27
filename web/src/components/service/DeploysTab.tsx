import { useCallback, useMemo, useRef, useState } from 'react';
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
import { usePanelRead } from '@/lib/use-panel-read.ts';

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
  canTrigger,
  canRecord,
  recordAs,
}: {
  service: ServiceView;
  onServiceChanged: () => void;
  onUnauthorized: () => void;
  // canTrigger shows deploy, cutover and rollback; canRecord shows the
  // manual record form. Both are asked of this service.
  canTrigger: boolean;
  canRecord: boolean;
  // recordAs is the username a manual record is filed under when the
  // server decides it (anyone but an admin); null lets the user type one.
  recordAs: string | null;
}) {
  const [sha, setSha] = useState('');
  const [actor, setActor] = useState('');
  const [outcome, setOutcome] = useState('success');
  const [notes, setNotes] = useState('');
  const [recording, setRecording] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [cursors, setCursors] = useState<number[]>([]);
  const [refresh, setRefresh] = useState(0);
  const [trendOpen, setTrendOpen] = useState(false);
  const [selected, setSelected] = useState<Deploy | null>(null);
  const detailTrigger = useRef<HTMLButtonElement | null>(null);
  const beforeId = cursors[cursors.length - 1];
  const readHistory = useCallback(
    (signal: AbortSignal) => fetchDeploys(service.id, signal, historyPageSize + 1, beforeId),
    [service.id, beforeId],
  );
  const readTrend = useCallback((signal: AbortSignal) => fetchDeploys(service.id, signal), [service.id]);
  const history = usePanelRead(`${service.id}:${beforeId ?? ''}`, readHistory, refresh, onUnauthorized);
  const trendRead = usePanelRead(service.id, trendOpen ? readTrend : null, refresh, onUnauthorized);

  const reloadLatest = useCallback(() => {
    setCursors([]);
    setRefresh((value) => value + 1);
  }, []);

  const handleOperationFinished = useCallback(() => {
    reloadLatest();
    onServiceChanged();
  }, [reloadLatest, onServiceChanged]);

  async function handleRecordDeploy(): Promise<void> {
    const recordedActor = recordAs ?? actor.trim();

    if (sha.trim() === '' || recordedActor === '') {
      setFormError(recordAs === null ? 'SHA and actor are required.' : 'SHA is required.');
      return;
    }

    setFormError(null);
    setRecording(true);

    try {
      await recordDeploy(service.id, {
        sha: sha.trim(),
        actor: recordedActor,
        outcome,
        notes: notes.trim(),
      });
      setSha('');
      setActor('');
      setNotes('');
      toast.success('Deploy recorded.');
      reloadLatest();
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
      (trendRead.data ?? [])
        .filter((deploy) => deploy.downtime_secs !== null)
        .slice(0, 20)
        .reverse()
        .map((deploy) => ({
          label: `#${deploy.id}`,
          downtime: deploy.downtime_secs ?? 0,
        })),
    [trendRead.data],
  );

  const rows = history.data?.slice(0, historyPageSize) ?? [];
  const nextCursor = (history.data?.length ?? 0) > historyPageSize ? rows[rows.length - 1]?.id : undefined;

  return (
    <div className="space-y-4">
      {canTrigger && service.strategy === 'bluegreen' && ['', 'unknown'].includes(service.live_color) && (
        <div className="border-border rounded-lg border p-4 text-sm" role="status">
          <p className="font-medium">Live target could not be verified</p>
          <p className="text-muted-foreground mt-1">
            Check that the configured Nginx active marker appears on one upstream server line
            and points to a configured target. Cutover and rollback become available after Touch Grass verifies it.
          </p>
        </div>
      )}
      {canTrigger && service.strategy === 'bluegreen' && (
        <CutoverPanel service={service} onFinished={handleOperationFinished} />
      )}

      {canTrigger && service.strategy === 'recreate' && (
        <DeployPanel service={service} onFinished={handleOperationFinished} />
      )}

      {canTrigger && (service.strategy === 'bluegreen' || service.strategy === 'recreate') && (
        <RollbackPanel service={service} onFinished={handleOperationFinished} />
      )}

      <details className="rounded-lg border" onToggle={(event) => setTrendOpen(event.currentTarget.open)}>
        <summary className="cursor-pointer p-4 text-sm font-medium">Downtime per deploy</summary>
        {trendOpen && <div className="p-4 pt-0">
          {trendRead.error && (
            <ErrorState title="Couldn't load downtime samples" message={trendRead.error} onRetry={trendRead.retry} />
          )}
          {trendRead.data === null ? !trendRead.error && (
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
        </div>}
      </details>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">History</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {history.error && (
            <ErrorState title="Couldn't load deploy history" message={history.error} onRetry={history.retry} />
          )}
          {history.data === null ? !history.error && (
            <div className="space-y-2">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : rows.length === 0 ? (
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
                  <TableRow key={deploy.id}>
                    <TableCell className="font-mono" title={deploy.notes === '' ? undefined : deploy.notes}>
                      <button type="button" className="underline-offset-4 hover:underline focus-visible:underline"
                        onClick={(event) => {
                          detailTrigger.current = event.currentTarget;
                          setSelected(deploy);
                        }}>
                        {deploy.sha.slice(0, 12)}
                      </button>
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

          {(cursors.length > 0 || nextCursor !== undefined) && (
            <div className="flex items-center justify-between text-sm">
              <p className="text-muted-foreground">Page {cursors.length + 1}</p>
              <div className="flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={cursors.length === 0}
                  onClick={() => setCursors((previous) => previous.slice(0, -1))}
                >
                  Previous
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={nextCursor === undefined}
                  onClick={() => {
                    if (nextCursor !== undefined) setCursors((previous) => [...previous, nextCursor]);
                  }}
                >
                  Next
                </Button>
              </div>
            </div>
          )}

          {canRecord && formError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {formError}
            </p>
          )}

          {canRecord && (
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
            {recordAs === null ? (
              <div className="space-y-1">
                <Label htmlFor={`actor-${service.id}`}>Actor</Label>
                <Input
                  id={`actor-${service.id}`}
                  value={actor}
                  onChange={(event) => setActor(event.target.value)}
                  className="w-32"
                />
              </div>
            ) : (
              <div className="space-y-1">
                <p className="text-sm leading-none font-medium">Actor</p>
                <p className="text-muted-foreground flex h-9 items-center text-sm">
                  Recorded as {recordAs}
                </p>
              </div>
            )}
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
          )}
        </CardContent>
      </Card>

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="overflow-y-auto sm:max-w-md" onCloseAutoFocus={(event) => {
          if (detailTrigger.current?.isConnected) {
            event.preventDefault();
            detailTrigger.current.focus();
          }
        }}>
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
