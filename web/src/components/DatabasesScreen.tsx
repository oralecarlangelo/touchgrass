import { useCallback, useEffect, useMemo, useState } from 'react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from './ErrorState.tsx';
import HeartbeatStatus from './HeartbeatStatus.tsx';
import {
  fetchDatabaseBackups,
  fetchDatabaseJob,
  fetchDatabaseJobs,
  fetchDatabases,
  isUnauthorized,
  startDatabaseBackup,
  startDatabaseRestore,
  verifyDatabaseBackup,
  type DatabaseBackup,
  type DatabaseJob,
  type DatabasesResponse,
  type DatabaseView,
} from '@/lib/api.ts';
import { LatestRequest } from '@/lib/latest-request.ts';
import { formatBytes, formatDuration, formatTime, timeAgo } from '@/lib/format.ts';

const jobPollMs = 2_000;
const refreshIntervalMs = 30_000;

type VerifyState = 'verifying' | 'ok' | 'bad';

function isTerminal(status: string): boolean {
  return status !== 'running';
}

function DatabaseStatus({ status }: { status: string }) {
  const variant = status === 'healthy' ? 'default' :
    ['unavailable', 'unhealthy', 'missing'].includes(status) ? 'destructive' : 'secondary';
  return <Badge variant={variant}>{status}</Badge>;
}

function DatabaseCard({ database, action }: { database: DatabaseView; action?: React.ReactNode }) {
  const discovered = database.id.startsWith('docker:');
  const details = [
    database.image,
    database.version,
    database.size_bytes == null ? undefined : formatBytes(database.size_bytes),
  ].filter(Boolean).join(' · ');
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-sm font-medium">{database.label}</CardTitle>
          <DatabaseStatus status={database.status} />
          {discovered && <Badge variant="outline">Discovered</Badge>}
          {action}
        </div>
      </CardHeader>
      <CardContent className="text-muted-foreground space-y-1 text-sm">
        {database.container && <p className="font-mono text-xs break-all">{database.container}</p>}
        {details && <p className="font-mono text-xs break-all">{details}</p>}
        {database.observed_at && (
          <p title={formatTime(database.observed_at)}>Observed {timeAgo(database.observed_at)}</p>
        )}
      </CardContent>
    </Card>
  );
}

function JobStatusBadge({ status }: { status: string }) {
  if (status === 'success') {
    return <Badge variant="default">success</Badge>;
  }

  if (status === 'running') {
    return <Badge variant="secondary">running</Badge>;
  }

  if (status === 'failed') {
    return <Badge variant="destructive">failed</Badge>;
  }

  return <Badge variant="outline">{status}</Badge>;
}

function jobDuration(job: DatabaseJob): string {
  if (job.finished_at === null || job.finished_at === undefined || job.finished_at === '') {
    return '—';
  }

  const started = new Date(job.started_at).getTime();
  const finished = new Date(job.finished_at).getTime();

  if (Number.isNaN(started) || Number.isNaN(finished) || finished < started) {
    return '—';
  }

  return formatDuration((finished - started) / 1000);
}

export default function DatabasesScreen({
  onUnauthorized,
  isAdmin,
}: {
  onUnauthorized: () => void;
  isAdmin: boolean;
}) {
  const [snapshot, setSnapshot] = useState<DatabasesResponse | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [dbError, setDbError] = useState<string | null>(null);
  const [backups, setBackups] = useState<DatabaseBackup[] | null>(null);
  const [backupsError, setBackupsError] = useState<string | null>(null);
  const [jobs, setJobs] = useState<DatabaseJob[] | null>(null);
  const [jobsError, setJobsError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [startingBackup, setStartingBackup] = useState(false);
  const [trackedId, setTrackedId] = useState<number | string | null>(null);
  const [tracked, setTracked] = useState<DatabaseJob | null>(null);
  const [verify, setVerify] = useState<Record<string, VerifyState>>({});
  const [restoreName, setRestoreName] = useState<string | null>(null);
  const [restoreConfirm, setRestoreConfirm] = useState('');
  const [restoreStarting, setRestoreStarting] = useState(false);
  const [restoreJob, setRestoreJob] = useState<DatabaseJob | null>(null);
  const [restoreDone, setRestoreDone] = useState(false);

  const requests = useMemo(() => ({
    health: new LatestRequest<void>(),
    backups: new LatestRequest<void>(),
    jobs: new LatestRequest<void>(),
  }), []);

  const loadHealth = useCallback(() => requests.health.run(async (signal) => {
    setRefreshing(true);
    setDbError(null);
    try {
      const result = await fetchDatabases(signal);
      if (!signal.aborted) setSnapshot(result);
    } catch (err) {
      if (signal.aborted) return;
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setDbError(err instanceof Error ? err.message : String(err));
    } finally {
      if (!signal.aborted) setRefreshing(false);
    }
  }), [onUnauthorized, requests]);

  const loadBackups = useCallback(() => requests.backups.run(async (signal) => {
    setBackupsError(null);
    try {
      const result = await fetchDatabaseBackups(signal);
      if (!signal.aborted) setBackups(result);
    } catch (err) {
      if (signal.aborted) return;
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setBackupsError(err instanceof Error ? err.message : String(err));
    }
  }), [onUnauthorized, requests]);

  const loadJobs = useCallback(() => requests.jobs.run(async (signal) => {
    setJobsError(null);
    try {
      const result = await fetchDatabaseJobs(signal);
      if (!signal.aborted) setJobs(result);
    } catch (err) {
      if (signal.aborted) return;
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setJobsError(err instanceof Error ? err.message : String(err));
    }
  }), [onUnauthorized, requests]);

  useEffect(() => {
    const refreshVisible = () => {
      if (!document.hidden) void loadHealth();
    };
    refreshVisible();
    const timer = setInterval(refreshVisible, refreshIntervalMs);
    document.addEventListener('visibilitychange', refreshVisible);

    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', refreshVisible);
      requests.health.cancel();
      requests.backups.cancel();
      requests.jobs.cancel();
    };
  }, [loadHealth, requests]);

  useEffect(() => {
    if (historyOpen) {
      void loadBackups();
      void loadJobs();
    }
  }, [historyOpen, loadBackups, loadJobs]);

  // Poll a running backup job every 2s until it reaches a terminal status.
  useEffect(() => {
    if (trackedId === null) {
      return;
    }

    let cancelled = false;
    const jobId = trackedId;

    async function tick(): Promise<void> {
      try {
        const job = await fetchDatabaseJob(jobId);

        if (cancelled) {
          return;
        }

        setTracked(job);

        if (!isTerminal(job.status)) {
          return;
        }

        setTrackedId(null);

        if (job.status === 'success') {
          toast.success(`Backup finished: ${job.target === '' ? job.detail : job.target}.`);
        } else {
          toast.error(`Backup ${job.status}: ${job.detail === '' ? job.target : job.detail}`);
        }

        await Promise.all([loadHealth(), loadBackups(), loadJobs()]);
      } catch (err) {
        if (cancelled) {
          return;
        }

        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setTrackedId(null);
        toast.error(err instanceof Error ? err.message : String(err));
      }
    }

    void tick();
    const timer = setInterval(() => void tick(), jobPollMs);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [trackedId, loadHealth, loadBackups, loadJobs, onUnauthorized]);

  // Poll a running restore job the same way, shown inside the restore modal.
  useEffect(() => {
    if (restoreName === null || restoreJob === null || isTerminal(restoreJob.status)) {
      return;
    }

    let cancelled = false;
    const jobId = restoreJob.id;

    async function tick(): Promise<void> {
      try {
        const job = await fetchDatabaseJob(jobId);

        if (cancelled) {
          return;
        }

        setRestoreJob(job);

        if (!isTerminal(job.status)) {
          return;
        }

        setRestoreDone(true);

        if (job.status === 'success') {
          toast.success(`Restore finished: ${job.detail === '' ? job.target : job.detail}`);
        } else {
          toast.error(`Restore ${job.status}: ${job.detail === '' ? job.target : job.detail}`);
        }

        await Promise.all([loadHealth(), loadBackups(), loadJobs()]);
      } catch (err) {
        if (cancelled) {
          return;
        }

        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setRestoreDone(true);
        toast.error(err instanceof Error ? err.message : String(err));
      }
    }

    const timer = setInterval(() => void tick(), jobPollMs);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [restoreName, restoreJob, loadHealth, loadBackups, loadJobs, onUnauthorized]);

  async function handleBackup(): Promise<void> {
    setStartingBackup(true);

    try {
      const started = await startDatabaseBackup();
      setTracked(null);
      setTrackedId(started.id);
      toast.success('Backup started — polling for progress.');
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setStartingBackup(false);
    }
  }

  async function handleVerify(name: string): Promise<void> {
    setVerify((current) => ({ ...current, [name]: 'verifying' }));

    try {
      const result = await verifyDatabaseBackup(name);
      setVerify((current) => ({ ...current, [name]: result.ok ? 'ok' : 'bad' }));

      if (result.ok) {
        toast.success(`${name} verified against its checksum.`);
      } else {
        toast.error(`${name} failed verification — treat it as suspect.`);
      }
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setVerify((current) => {
        const next = { ...current };
        delete next[name];
        return next;
      });
      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  function openRestore(name: string): void {
    setRestoreName(name);
    setRestoreConfirm('');
    setRestoreStarting(false);
    setRestoreJob(null);
    setRestoreDone(false);
  }

  function closeRestore(): void {
    setRestoreName(null);
    setRestoreConfirm('');
    setRestoreStarting(false);
    setRestoreJob(null);
    setRestoreDone(false);
  }

  async function handleRestore(): Promise<void> {
    if (restoreName === null) {
      return;
    }

    setRestoreStarting(true);

    try {
      const started = await startDatabaseRestore(restoreName);
      // Detail fills in on the first poll tick; seed a running row meanwhile.
      setRestoreJob({
        id: started.id,
        kind: 'restore',
        target: restoreName,
        status: started.status,
        detail: 'Restore started…',
        started_at: new Date().toISOString(),
      });
      setRestoreDone(isTerminal(started.status));
      await loadJobs();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setRestoreStarting(false);
    }
  }

  const databases = snapshot?.databases ?? [];
  const postgres = databases.find((db) => db.id === 'postgres');
  const backupReady = postgres?.configured === true && ['healthy', 'running'].includes(postgres.status);
  const backupRunning = trackedId !== null || tracked?.status === 'running';
  const sortedBackups = [...(backups ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const restoreMatches = restoreName !== null && restoreConfirm === restoreName;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold tracking-tight">Databases</h1>
        <Button variant="outline" size="sm" onClick={() => void loadHealth()} disabled={refreshing}>
          {refreshing ? 'Checking…' : 'Refresh heartbeat'}
        </Button>
      </div>

      {dbError !== null && (
        <ErrorState title="Couldn't load database health" message={dbError} onRetry={() => void loadHealth()} />
      )}
      {snapshot === null && dbError === null && (
        <Skeleton className="h-24 w-full" aria-label="Loading database health" />
      )}
      {snapshot !== null && (
        <>
          <div className="rounded-md border p-3">
            <HeartbeatStatus
              label="Database discovery heartbeat"
              state={snapshot.inventory_status}
              observedAt={snapshot.docker_observed_at}
              detail={snapshot.inventory_status === 'waiting' ? 'Waiting for the first Docker inventory.'
                : snapshot.inventory_status === 'unavailable' ? 'Check that Docker is running and Touch Grass can read its socket.'
                  : snapshot.inventory_status === 'stale' ? 'Showing the last observed containers. Check the Fleet collector before relying on their status.'
                    : undefined}
              action={snapshot.inventory_status === 'unavailable' || snapshot.inventory_status === 'stale' ? (
                <a href="#/fleet" className="inline-block underline underline-offset-4">Open Fleet</a>
              ) : null}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            {databases.filter((db) => db.configured || db.id.startsWith('docker:')).map((database) => (
              <DatabaseCard key={database.id} database={database} action={
                isAdmin && database.id === 'postgres' && backupReady ? (
                  <Button
                    size="sm"
                    className="ml-auto"
                    onClick={() => void handleBackup()}
                    disabled={startingBackup || backupRunning}
                  >
                    {startingBackup ? 'Starting…' : backupRunning ? 'Backup running…' : 'Back up now'}
                  </Button>
                ) : undefined
              } />
            ))}
          </div>
          {snapshot.inventory_status === 'current' && !databases.some((db) => db.container) && (
            <p className="text-muted-foreground text-sm">No supported database containers detected.</p>
          )}
          {isAdmin && !postgres?.configured && (
            <p className="text-muted-foreground text-sm">
              Configure a PostgreSQL backup target to enable backups and restores.
            </p>
          )}
        </>
      )}

      {backupRunning && (
        <Card aria-live="polite">
          <CardContent className="flex flex-wrap items-center gap-3 pt-4 text-sm">
            <span
              className="bg-primary h-2 w-2 animate-pulse rounded-full"
              aria-hidden
            />
            <span className="font-medium">
              Backup {tracked === null ? 'starting' : 'running'}…
            </span>
            {tracked !== null && tracked.detail !== '' && (
              <span className="text-muted-foreground font-mono text-xs">{tracked.detail}</span>
            )}
            <span className="text-muted-foreground ml-auto text-xs">
              Refreshing every 2s until it finishes.
            </span>
          </CardContent>
        </Card>
      )}

      <details className="rounded-lg border" onToggle={(event) => setHistoryOpen(event.currentTarget.open)}>
        <summary className="cursor-pointer p-4 text-sm font-medium">Backups and job history</summary>
        {historyOpen && (
          <div className="space-y-4 p-4 pt-0">
            <Button variant="outline" size="sm" onClick={() => { void loadBackups(); void loadJobs(); }}>
              Refresh history
            </Button>
            <Card>
              <CardHeader>
                <CardTitle className="text-sm font-medium">Backups (newest first)</CardTitle>
              </CardHeader>
              <CardContent>
                {backupsError !== null ? (
                  <ErrorState
                    title="Couldn't load backups"
                    message={backupsError}
                    onRetry={() => void loadBackups()}
                  />
                ) : backups === null ? (
                  <div className="space-y-2" aria-label="Loading backups">
                    <Skeleton className="h-10 w-full" />
                    <Skeleton className="h-10 w-full" />
                  </div>
                ) : sortedBackups.length === 0 ? (
                  <p className="text-muted-foreground py-6 text-center text-sm">
                    No backups recorded.
                  </p>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Name</TableHead>
                        <TableHead className="text-right">Size</TableHead>
                        <TableHead>Age</TableHead>
                        <TableHead>Checksum</TableHead>
                        <TableHead className="text-right">Actions</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {sortedBackups.map((backup) => (
                        <TableRow key={backup.name}>
                          <TableCell className="font-mono text-xs">{backup.name}</TableCell>
                          <TableCell className="text-right font-mono">
                            {formatBytes(backup.size_bytes)}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-xs" title={formatTime(backup.created_at)}>
                            {timeAgo(backup.created_at)}
                          </TableCell>
                          <TableCell>
                            {backup.sha256 === 'present' ? (
                              <Badge variant="default">sha256</Badge>
                            ) : (
                              <Badge variant="secondary">missing</Badge>
                            )}
                          </TableCell>
                          <TableCell className="text-right">
                            <div className="flex items-center justify-end gap-2">
                              {verify[backup.name] === 'ok' && (
                                <Badge variant="default">verified</Badge>
                              )}
                              {verify[backup.name] === 'bad' && (
                                <Badge variant="destructive">checksum mismatch</Badge>
                              )}
                              {isAdmin && (
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  onClick={() => void handleVerify(backup.name)}
                                  disabled={verify[backup.name] === 'verifying'}
                                >
                                  {verify[backup.name] === 'verifying' ? 'Verifying…' : 'Verify'}
                                </Button>
                              )}
                              {isAdmin && (
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="text-destructive hover:text-destructive"
                                  onClick={() => openRestore(backup.name)}
                                  disabled={!backupReady || backupRunning}
                                >
                                  Restore
                                </Button>
                              )}
                            </div>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-sm font-medium">Jobs history</CardTitle>
              </CardHeader>
              <CardContent>
                {jobsError !== null ? (
                  <ErrorState
                    title="Couldn't load jobs"
                    message={jobsError}
                    onRetry={() => void loadJobs()}
                  />
                ) : jobs === null ? (
                  <div className="space-y-2" aria-label="Loading jobs">
                    <Skeleton className="h-10 w-full" />
                  </div>
                ) : jobs.length === 0 ? (
                  <p className="text-muted-foreground py-6 text-center text-sm">
                    No backup or restore jobs yet.
                  </p>
                ) : (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>ID</TableHead>
                        <TableHead>Kind</TableHead>
                        <TableHead>Target</TableHead>
                        <TableHead>Status</TableHead>
                        <TableHead>Started</TableHead>
                        <TableHead>Duration</TableHead>
                        <TableHead>Detail</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {jobs.map((job) => (
                        <TableRow key={String(job.id)}>
                          <TableCell className="font-mono text-xs">{String(job.id)}</TableCell>
                          <TableCell className="text-xs">{job.kind}</TableCell>
                          <TableCell className="max-w-48 truncate font-mono text-xs" title={job.target}>
                            {job.target}
                          </TableCell>
                          <TableCell>
                            <JobStatusBadge status={job.status} />
                          </TableCell>
                          <TableCell
                            className="text-muted-foreground text-xs"
                            title={formatTime(job.started_at)}
                          >
                            {timeAgo(job.started_at)}
                          </TableCell>
                          <TableCell className="font-mono text-xs">{jobDuration(job)}</TableCell>
                          <TableCell
                            className="text-muted-foreground max-w-64 truncate text-xs"
                            title={job.detail}
                          >
                            {job.detail === '' ? '—' : job.detail}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </CardContent>
            </Card>
          </div>
        )}
      </details>

      <Dialog open={restoreName !== null} onOpenChange={(open) => !open && closeRestore()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Restore Postgres from backup?</DialogTitle>
            <DialogDescription>
              Run restores off-peak: this takes several minutes and apps will see errors
              until it completes.
            </DialogDescription>
          </DialogHeader>

          {restoreJob === null ? (
            <>
              <div className="space-y-3 text-sm">
                <p>
                  Restoring <code className="bg-muted rounded px-1.5 py-0.5 font-mono text-xs">{restoreName}</code> will:
                </p>
                <ul className="text-muted-foreground list-disc space-y-1 pl-5">
                  <li>Take a pre-restore safety backup first.</li>
                  <li>Terminate other database connections for the restore.</li>
                  <li>Take several minutes — the job page polls until it finishes.</li>
                  <li>Surface errors in the apps until the restore completes.</li>
                </ul>
                <div className="space-y-1.5">
                  <Label htmlFor="restore-confirm">
                    Type the backup filename to confirm
                  </Label>
                  <Input
                    id="restore-confirm"
                    value={restoreConfirm}
                    onChange={(event) => setRestoreConfirm(event.target.value)}
                    placeholder={restoreName ?? ''}
                    autoComplete="off"
                    spellCheck={false}
                  />
                </div>
              </div>
              <DialogFooter>
                <Button variant="outline" onClick={closeRestore}>
                  Cancel
                </Button>
                <Button
                  variant="destructive"
                  onClick={() => void handleRestore()}
                  disabled={!restoreMatches || restoreStarting}
                >
                  {restoreStarting ? 'Starting…' : 'Start restore'}
                </Button>
              </DialogFooter>
            </>
          ) : (
            <div className="space-y-3 text-sm" aria-live="polite">
              <div className="flex items-center gap-2">
                <JobStatusBadge status={restoreJob.status} />
                <span className="font-mono text-xs">{restoreJob.target}</span>
              </div>
              <p className="text-muted-foreground font-mono text-xs">
                {restoreJob.detail === '' ? 'Working…' : restoreJob.detail}
              </p>
              {!restoreDone && !isTerminal(restoreJob.status) && (
                <p className="text-muted-foreground text-xs">
                  Polling every 2s — you can close this dialog; the job keeps running and
                  appears in the jobs history.
                </p>
              )}
              <DialogFooter>
                <Button variant="outline" onClick={closeRestore}>
                  {restoreDone || isTerminal(restoreJob.status) ? 'Close' : 'Close (keeps running)'}
                </Button>
              </DialogFooter>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
