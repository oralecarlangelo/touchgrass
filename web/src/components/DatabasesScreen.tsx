import { useCallback, useEffect, useState } from 'react';
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
  type DatabaseView,
} from '@/lib/api.ts';
import { formatBytes, formatDuration, formatTime, timeAgo } from '@/lib/format.ts';

const jobPollMs = 2_000;
const refreshIntervalMs = 30_000;

type VerifyState = 'verifying' | 'ok' | 'bad';

function isTerminal(status: string): boolean {
  return status !== 'running';
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="py-1.5">
      <p className="text-muted-foreground text-xs">{label}</p>
      <p className="font-mono text-sm">{value}</p>
    </div>
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

export default function DatabasesScreen({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [databases, setDatabases] = useState<DatabaseView[] | null>(null);
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

  const loadHealth = useCallback(async () => {
    setDbError(null);

    try {
      setDatabases(await fetchDatabases());
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setDbError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  const loadBackups = useCallback(async () => {
    setBackupsError(null);

    try {
      setBackups(await fetchDatabaseBackups());
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setBackupsError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  const loadJobs = useCallback(async () => {
    setJobsError(null);

    try {
      setJobs(await fetchDatabaseJobs());
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setJobsError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  const loadAll = useCallback(async () => {
    setRefreshing(true);

    try {
      await Promise.all([loadHealth(), loadBackups(), loadJobs()]);
    } finally {
      setRefreshing(false);
    }
  }, [loadHealth, loadBackups, loadJobs]);

  useEffect(() => {
    void loadAll();
    const timer = setInterval(() => void loadAll(), refreshIntervalMs);

    return () => clearInterval(timer);
  }, [loadAll]);

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

  const postgres = (databases ?? []).find((db) => db.id === 'postgres') ?? null;
  const redis = (databases ?? []).find((db) => db.id === 'redis') ?? null;
  const sqlite = (databases ?? []).find((db) => db.id === 'sqlite') ?? null;
  const backupRunning = trackedId !== null || tracked?.status === 'running';
  const sortedBackups = [...(backups ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const restoreMatches = restoreName !== null && restoreConfirm === restoreName;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold tracking-tight">Databases</h1>
        <Button variant="outline" size="sm" onClick={() => void loadAll()} disabled={refreshing}>
          {refreshing ? 'Checking…' : 'Refresh'}
        </Button>
      </div>

      {dbError !== null && databases === null ? (
        <ErrorState
          title="Couldn't load database health"
          message={dbError}
          onRetry={() => void loadHealth()}
        />
      ) : (
        <Card>
          <CardHeader>
            <div className="flex flex-wrap items-center gap-3">
              <CardTitle className="text-sm font-medium">
                {postgres === null ? 'PostgreSQL' : postgres.label}
              </CardTitle>
              {postgres !== null && postgres.configured && (
                <Badge variant={postgres.reachable ? 'default' : 'destructive'}>
                  {postgres.reachable ? 'reachable' : 'unreachable'}
                </Badge>
              )}
              {postgres !== null && postgres.configured && postgres.reachable && (
                <Button
                  size="sm"
                  className="ml-auto"
                  onClick={() => void handleBackup()}
                  disabled={startingBackup || backupRunning}
                >
                  {startingBackup ? 'Starting…' : backupRunning ? 'Backup running…' : 'Back up now'}
                </Button>
              )}
            </div>
          </CardHeader>
          <CardContent>
            {databases === null ? (
              <div className="space-y-2" aria-label="Loading database health">
                <Skeleton className="h-10 w-full" />
                <Skeleton className="h-10 w-full" />
              </div>
            ) : postgres === null || !postgres.configured ? (
              <div className="space-y-2">
                <p className="text-sm font-medium">Postgres isn&apos;t configured</p>
                <p className="text-muted-foreground text-sm">
                  Set the Postgres connection in the touchgrass environment to enable health
                  checks and backups:
                </p>
                <ul className="list-disc space-y-1 pl-5 font-mono text-xs">
                  <li>TOUCHGRASS_POSTGRES_CONTAINER (empty means unconfigured)</li>
                  <li>TOUCHGRASS_POSTGRES_USER</li>
                  <li>TOUCHGRASS_POSTGRES_DB</li>
                  <li>TOUCHGRASS_DB_BACKUP_DIR</li>
                </ul>
              </div>
            ) : !postgres.reachable ? (
              <ErrorState
                title="Postgres is unreachable"
                message="The container may be stopped or the credentials wrong. Check the TOUCHGRASS_POSTGRES_* environment."
                onRetry={() => void loadHealth()}
              />
            ) : (
              <div className="grid gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
                <Stat label="Version" value={postgres.version ?? 'n/a'} />
                <Stat
                  label="Size"
                  value={
                    postgres.size_bytes === null || postgres.size_bytes === undefined
                      ? 'n/a'
                      : formatBytes(postgres.size_bytes)
                  }
                />
                <Stat
                  label="Connections"
                  value={
                    postgres.connections_used === null ||
                    postgres.connections_used === undefined ||
                    postgres.connections_max === null ||
                    postgres.connections_max === undefined
                      ? 'n/a'
                      : `${postgres.connections_used} / ${postgres.connections_max}`
                  }
                />
                <Stat
                  label="Uptime"
                  value={
                    postgres.uptime_secs === null || postgres.uptime_secs === undefined
                      ? 'n/a'
                      : formatDuration(postgres.uptime_secs)
                  }
                />
                <Stat
                  label="Last backup"
                  value={
                    postgres.last_backup_at === null ||
                    postgres.last_backup_at === undefined ||
                    postgres.last_backup_at === ''
                      ? 'never'
                      : `${formatTime(postgres.last_backup_at)} (${timeAgo(postgres.last_backup_at)})`
                  }
                />
              </div>
            )}
          </CardContent>
        </Card>
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

      {(redis?.configured === true || sqlite?.configured === true) && (
        <div className="grid gap-4 sm:grid-cols-2">
          {redis?.configured === true && (
            <Card>
              <CardHeader className="pb-2">
                <div className="flex items-center gap-2">
                  <CardTitle className="text-sm font-medium">{redis.label}</CardTitle>
                  <Badge variant={redis.reachable ? 'default' : 'destructive'}>
                    {redis.reachable ? 'reachable' : 'unreachable'}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent className="text-muted-foreground text-sm">
                {redis.reachable ? (
                  <span className="font-mono text-xs">
                    {[redis.version ?? '', redis.used_memory_bytes == null ? '' : formatBytes(redis.used_memory_bytes)]
                      .filter((part) => part !== '')
                      .join(' · ') || 'healthy'}
                  </span>
                ) : (
                  'Redis is configured but not answering.'
                )}
              </CardContent>
            </Card>
          )}
          {sqlite?.configured === true && (
            <Card>
              <CardHeader className="pb-2">
                <div className="flex items-center gap-2">
                  <CardTitle className="text-sm font-medium">{sqlite.label}</CardTitle>
                  <Badge variant={sqlite.reachable ? 'default' : 'destructive'}>
                    {sqlite.reachable ? 'reachable' : 'unreachable'}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent className="text-muted-foreground text-sm">
                {sqlite.reachable ? (
                  <span className="font-mono text-xs">
                    {[
                      sqlite.integrity == null || sqlite.integrity === ''
                        ? ''
                        : `integrity: ${sqlite.integrity}`,
                      sqlite.size_bytes == null ? '' : formatBytes(sqlite.size_bytes),
                    ]
                      .filter((part) => part !== '')
                      .join(' · ') || 'healthy'}
                  </span>
                ) : (
                  "touchgrass's own database isn't answering."
                )}
              </CardContent>
            </Card>
          )}
        </div>
      )}

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
              No backups yet — take the first one from the Postgres card above.
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
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => void handleVerify(backup.name)}
                          disabled={verify[backup.name] === 'verifying'}
                        >
                          {verify[backup.name] === 'verifying' ? 'Verifying…' : 'Verify'}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-destructive hover:text-destructive"
                          onClick={() => openRestore(backup.name)}
                          disabled={backupRunning}
                        >
                          Restore
                        </Button>
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
