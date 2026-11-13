import { useCallback, useEffect, useRef, useState } from 'react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import EmptyState from '../EmptyState.tsx';
import ErrorState from '../ErrorState.tsx';
import MigrationCommandDialog, { type PendingCommand } from '../migrations/MigrationCommandDialog.tsx';
import MigrationRunsCard, { SeedCard } from '../migrations/MigrationRunsCard.tsx';
import MigrationSetupDialog from '../migrations/MigrationSetupDialog.tsx';
import MigrationStatusCard from '../migrations/MigrationStatusCard.tsx';
import MigrationsTable from '../migrations/MigrationsTable.tsx';
import {
  deleteMigrationConfig,
  fetchMigrationConfig,
  fetchMigrationRun,
  fetchMigrationRuns,
  fetchMigrationStatus,
  isUnauthorized,
  saveMigrationConfig,
  startMigrationDeploy,
  startMigrationResolve,
  startMigrationSeed,
  type Migration,
  type MigrationConfigInput,
  type MigrationConfigResponse,
  type MigrationRun,
  type MigrationStatus,
  type ServiceView,
} from '@/lib/api.ts';
import { hasRunningRun, runKindLabels } from '@/lib/migrations.ts';

const runPollMs = 2000;

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// MigrationsView is everything the tab shows once the status is known.
// It holds no state, so each role's view is tested by rendering it.
export function MigrationsView({
  status,
  runs,
  canManage,
  onRefresh,
  onSetup,
  onCommand,
  onOutput,
}: {
  status: MigrationStatus;
  runs: MigrationRun[];
  canManage: boolean;
  onRefresh: () => void;
  onSetup: () => void;
  onCommand: (command: PendingCommand) => void;
  onOutput: (run: MigrationRun) => void;
}) {
  if (!status.configured) {
    return (
      <EmptyState
        title="Migrations are not set up for this service"
        body={
          canManage
            ? 'Name the migration tool and the database that holds its history, and touchgrass will show which migrations are applied, pending, failed or edited.'
            : 'An admin has not set up migration tracking for this service yet.'
        }
        action={canManage ? <Button onClick={onSetup}>Set up migrations</Button> : undefined}
      />
    );
  }

  const busy = hasRunningRun(runs);

  return (
    <div className="space-y-4">
      {busy && (
        <p role="status" className="bg-muted rounded-md px-3 py-2 text-sm">
          A command is running for this service. The status updates when it finishes.
        </p>
      )}
      <MigrationStatusCard
        status={status}
        canManage={canManage}
        busy={busy}
        onRefresh={onRefresh}
        onSetup={onSetup}
        onDeploy={() => onCommand({ kind: 'deploy', pending: status.counts.pending })}
      />
      <MigrationsTable
        migrations={status.migrations}
        canManage={canManage}
        busy={busy}
        onResolve={(migration: Migration, resolution) =>
          onCommand({ kind: 'resolve', migration: migration.name, resolution })
        }
      />
      <SeedCard
        seed={status.seed}
        runs={runs}
        canManage={canManage}
        busy={busy}
        onSeed={() => onCommand({ kind: 'seed', command: status.seed.command ?? '' })}
      />
      <MigrationRunsCard runs={runs} canManage={canManage} onOutput={onOutput} />
    </div>
  );
}

export default function MigrationsTab({
  service,
  canManage,
  onUnauthorized,
}: {
  service: ServiceView;
  // canManage shows the setup, the commands and run output (admin).
  canManage: boolean;
  onUnauthorized: () => void;
}) {
  const [status, setStatus] = useState<MigrationStatus | null>(null);
  const [runs, setRuns] = useState<MigrationRun[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [setup, setSetup] = useState<MigrationConfigResponse | null>(null);
  const [setupOpen, setSetupOpen] = useState(false);
  const [command, setCommand] = useState<PendingCommand | null>(null);
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [output, setOutput] = useState<MigrationRun | null>(null);
  const wasRunning = useRef(false);

  const fail = useCallback(
    (error: unknown): string | null => {
      if (isUnauthorized(error)) {
        onUnauthorized();

        return null;
      }

      return messageOf(error);
    },
    [onUnauthorized],
  );

  const load = useCallback(async () => {
    try {
      const [nextStatus, nextRuns] = await Promise.all([
        fetchMigrationStatus(service.id),
        fetchMigrationRuns(service.id),
      ]);

      setStatus(nextStatus);
      setRuns(nextRuns);
      setLoadError(null);
    } catch (error) {
      setLoadError(fail(error));
    }
  }, [service.id, fail]);

  useEffect(() => {
    setStatus(null);
    setRuns([]);
    void load();
  }, [load]);

  // While a command runs, watch the run list; when it stops, the
  // status is stale and is read again.
  useEffect(() => {
    const running = hasRunningRun(runs);

    if (!running) {
      if (wasRunning.current) {
        wasRunning.current = false;
        void load();
      }

      return undefined;
    }

    wasRunning.current = true;

    const timer = window.setTimeout(() => {
      fetchMigrationRuns(service.id)
        .then(setRuns)
        .catch((error: unknown) => {
          fail(error);
        });
    }, runPollMs);

    return () => window.clearTimeout(timer);
  }, [runs, service.id, load, fail]);

  const openSetup = useCallback(() => {
    setDialogError(null);
    setSetup(null);
    setSetupOpen(true);
    fetchMigrationConfig(service.id)
      .then(setSetup)
      .catch((error: unknown) => setDialogError(fail(error)));
  }, [service.id, fail]);

  const saveSetup = useCallback(
    async (input: MigrationConfigInput) => {
      setBusy(true);
      setDialogError(null);

      try {
        await saveMigrationConfig(service.id, input);
        setSetupOpen(false);
        toast.success('Migration setup saved');
        await load();
      } catch (error) {
        setDialogError(fail(error));
      } finally {
        setBusy(false);
      }
    },
    [service.id, load, fail],
  );

  const removeSetup = useCallback(async () => {
    setBusy(true);
    setDialogError(null);

    try {
      await deleteMigrationConfig(service.id);
      setSetupOpen(false);
      toast.success('Migration setup removed');
      await load();
    } catch (error) {
      setDialogError(fail(error));
    } finally {
      setBusy(false);
    }
  }, [service.id, load, fail]);

  const runCommand = useCallback(
    async (confirm: string) => {
      if (command === null) {
        return;
      }

      setBusy(true);
      setDialogError(null);

      try {
        const run =
          command.kind === 'deploy'
            ? await startMigrationDeploy(service.id, confirm)
            : command.kind === 'seed'
              ? await startMigrationSeed(service.id, confirm)
              : await startMigrationResolve(service.id, {
                  confirm,
                  migration: command.migration,
                  resolution: command.resolution,
                });

        setCommand(null);
        setRuns((current) => [run, ...current]);
        toast.success(`${runKindLabels[run.kind]} started`);
      } catch (error) {
        setDialogError(fail(error));
      } finally {
        setBusy(false);
      }
    },
    [command, service.id, fail],
  );

  const showOutput = useCallback(
    (run: MigrationRun) => {
      setOutput(run);
      fetchMigrationRun(service.id, run.id)
        .then(setOutput)
        .catch((error: unknown) => {
          const message = fail(error);

          if (message !== null) {
            toast.error(message);
          }
        });
    },
    [service.id, fail],
  );

  if (loadError !== null) {
    return <ErrorState title="Could not load migrations" message={loadError} onRetry={() => void load()} />;
  }

  if (status === null) {
    return (
      <div className="space-y-4" aria-busy="true">
        <Skeleton className="h-36 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  }

  return (
    <>
      <MigrationsView
        status={status}
        runs={runs}
        canManage={canManage}
        onRefresh={() => void load()}
        onSetup={openSetup}
        onCommand={(next) => {
          setDialogError(null);
          setCommand(next);
        }}
        onOutput={showOutput}
      />
      <MigrationSetupDialog
        open={setupOpen}
        setup={setup}
        serviceName={service.name}
        busy={busy}
        error={dialogError}
        onClose={() => setSetupOpen(false)}
        onSave={(input) => void saveSetup(input)}
        onRemove={() => void removeSetup()}
      />
      <MigrationCommandDialog
        command={command}
        serviceName={service.name}
        database={status.history.target}
        container={status.shipped.target.split(':')[0] ?? ''}
        busy={busy}
        error={dialogError}
        onClose={() => setCommand(null)}
        onConfirm={(confirm) => void runCommand(confirm)}
      />
      <Sheet open={output !== null} onOpenChange={(next) => { if (!next) setOutput(null); }}>
        <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle>{output === null ? '' : `${runKindLabels[output.kind]} · run ${output.id}`}</SheetTitle>
            <SheetDescription className="font-mono text-xs break-all">{output?.detail}</SheetDescription>
          </SheetHeader>
          <div className="px-4 pb-4">
            {output?.output_truncated === true && (
              <p className="text-muted-foreground mb-2 text-xs">Only the end of the output was kept.</p>
            )}
            <pre className="bg-muted max-h-[70vh] overflow-auto rounded p-3 text-xs whitespace-pre-wrap">
              {output?.output !== undefined && output.output !== ''
                ? output.output
                : output?.state === 'running'
                  ? 'Still running. Output is stored when the command finishes.'
                  : 'No output.'}
            </pre>
          </div>
        </SheetContent>
      </Sheet>
    </>
  );
}
