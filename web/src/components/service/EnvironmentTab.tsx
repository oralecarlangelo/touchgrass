import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { History, Lock, MoreHorizontal, RefreshCw, Search, Upload } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from '../ErrorState.tsx';
import EnvAddRow from '../env/EnvAddRow.tsx';
import EnvApplyProgress from '../env/EnvApplyProgress.tsx';
import { EnvNoEntries, EnvNotConfigured } from '../env/EnvEmptyStates.tsx';
import EnvEntriesTable, { type EnvEntryActions } from '../env/EnvEntriesTable.tsx';
import EnvHelp from '../env/EnvHelp.tsx';
import EnvPasteSheet, { type StagedPasteLine } from '../env/EnvPasteSheet.tsx';
import EnvPendingBar from '../env/EnvPendingBar.tsx';
import EnvReviewDialog from '../env/EnvReviewDialog.tsx';
import EnvRevisionsSheet from '../env/EnvRevisionsSheet.tsx';
import EnvValidationPanel from '../env/EnvValidationPanel.tsx';
import { useEnvApplyRun } from '@/hooks/useEnvApplyRun.ts';
import {
  ApiError,
  apiErrorCode,
  applyEnv,
  deleteEnvEntry,
  discardEnvChanges,
  fetchEnv,
  fetchEnvDiff,
  fetchEnvRevisions,
  importEnvFile,
  isUnauthorized,
  putEnvEntry,
  revealEnvEntry,
  rollbackEnvRevision,
  type EnvDiff,
  type EnvEntry,
  type EnvImportResult,
  type EnvOverview,
  type EnvRevision,
  type ServiceView,
} from '@/lib/api.ts';
import {
  defaultEnvFilters,
  envShortcut,
  filterEnvEntries,
  initialRevealState,
  missingRequiredKeys,
  pendingCounts,
  revealReducer,
  sortEnvEntries,
  undoPlan,
  type ApplyRunState,
  type EnvFilters,
  type EnvSort,
  type RevealState,
} from '@/lib/env.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

const inProgressPollMs = 3_000;

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function FilterChip({
  label,
  active,
  count,
  onClick,
}: {
  label: string;
  active: boolean;
  count?: number;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      size="xs"
      variant={active ? 'secondary' : 'outline'}
      aria-pressed={active}
      onClick={onClick}
    >
      {label}
      {count !== undefined && count > 0 && (
        <span className="text-muted-foreground tabular-nums">{count}</span>
      )}
    </Button>
  );
}

export interface EnvironmentTabViewProps {
  service: ServiceView;
  isAdmin: boolean;
  overview: EnvOverview | null;
  loading: boolean;
  error: string | null;
  run: ApplyRunState | null;
  runKind: 'apply' | 'rollback';
  streamLost: boolean;
  now: number;
  filters: EnvFilters;
  sort: EnvSort;
  busyKey: string | null;
  addBusy: boolean;
  addError: string | null;
  prefillKey: string | null;
  importBusy: boolean;
  importError: string | null;
  importResult: EnvImportResult | null;
  reveal: RevealState;
  viewerNotice: boolean;
  searchRef?: React.RefObject<HTMLInputElement | null>;
  onRetry: () => void;
  onFilters: (filters: EnvFilters) => void;
  onSort: (sort: EnvSort) => void;
  onStage: (key: string, value: string, isSecret: boolean) => void;
  onPaste: (text: string) => void;
  onAddKey: (key: string) => void;
  onImport: (replace: boolean) => void;
  onOpenRevisions: () => void;
  onReview: () => void;
  onDiscard: () => void;
  onRetryRun: () => void;
  onDismissRun: () => void;
  entryActions: EnvEntryActions;
}

// EnvironmentTabView renders every state of the tab from props. The
// container below owns the data; this split keeps the states testable.
export function EnvironmentTabView(props: EnvironmentTabViewProps) {
  const { service, isAdmin, overview, loading, error, run, filters, sort } = props;
  const runRunning = run !== null && run.status === 'running';
  const applyInProgress = runRunning || (overview?.apply_in_progress ?? false);
  const readOnly = !isAdmin || applyInProgress;

  const entries = overview?.entries ?? [];
  const visible = useMemo(
    () => sortEnvEntries(filterEnvEntries(entries, filters), sort),
    [entries, filters, sort],
  );
  const counts = useMemo(() => pendingCounts(entries), [entries]);
  const missing = useMemo(
    () => missingRequiredKeys(overview?.last_validation ?? null, entries),
    [overview, entries],
  );
  const existingKeys = useMemo(() => new Set(entries.map((entry) => entry.key)), [entries]);
  const secretCount = entries.filter((entry) => entry.is_secret).length;
  const requiredCount = entries.filter((entry) => entry.required).length;
  const anyFilter = filters.pendingOnly || filters.secretsOnly || filters.requiredOnly || filters.query !== '';

  if (error !== null && overview === null) {
    return <ErrorState title="Couldn't load the environment" message={error} onRetry={props.onRetry} />;
  }

  if (loading && overview === null) {
    return (
      <div className="space-y-3" aria-label="Loading environment">
        <Skeleton className="h-8 w-72" />
        <Skeleton className="h-4 w-96" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (overview === null) {
    return null;
  }

  if (!overview.configured) {
    return (
      <div className="space-y-4">
        <EnvNotConfigured reason={overview.reason} serviceId={service.id} isAdmin={isAdmin} />
      </div>
    );
  }

  return (
    <div className="space-y-4 pb-16">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold tracking-tight">Environment</h2>
          <p className="text-muted-foreground text-sm">
            {overview.target !== null ? (
              <>
                <span className="text-foreground font-mono text-xs">{overview.target.path}</span>
                {' · '}
                {overview.target.owner} {overview.target.mode}
              </>
            ) : (
              'Target file on the host'
            )}
            {overview.applied_revision !== null && (
              <>
                {' · applied '}
                <time
                  dateTime={overview.applied_revision.created_at}
                  title={formatTime(overview.applied_revision.created_at)}
                >
                  {timeAgo(overview.applied_revision.created_at) || formatTime(overview.applied_revision.created_at)}
                </time>
                {overview.applied_revision.created_by !== '' && ` by ${overview.applied_revision.created_by}`}
              </>
            )}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button type="button" variant="outline" size="sm" onClick={props.onRetry} disabled={loading} aria-label="Refresh environment">
            <RefreshCw className={loading ? 'animate-spin' : ''} aria-hidden />
            Refresh
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={props.onOpenRevisions}>
            <History aria-hidden />
            Revisions
          </Button>
          {isAdmin && entries.length > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button type="button" variant="outline" size="icon-sm" aria-label="More actions">
                  <MoreHorizontal aria-hidden />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem disabled={readOnly || props.importBusy} onSelect={() => props.onImport(true)}>
                  <Upload aria-hidden />
                  Re-import from the host file
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      </div>

      {run !== null && (
        <EnvApplyProgress
          run={run}
          kind={props.runKind}
          now={props.now}
          streamLost={props.streamLost}
          onRetry={run.status === 'failed' && props.runKind === 'apply' && isAdmin ? props.onRetryRun : null}
          onDismiss={props.onDismissRun}
        />
      )}

      {run === null && overview.apply_in_progress && (
        <Alert>
          <RefreshCw className="animate-spin" aria-hidden />
          <AlertTitle>An apply is running</AlertTitle>
          <AlertDescription>
            Another session started it. The table is read-only until it finishes; this page refreshes on its own.
          </AlertDescription>
        </Alert>
      )}

      {props.viewerNotice && (
        <Alert>
          <Lock aria-hidden />
          <AlertTitle>Read-only account</AlertTitle>
          <AlertDescription>Viewers can see keys and plain values. Ask an admin to change entries.</AlertDescription>
        </Alert>
      )}

      {error !== null && (
        <Alert variant="destructive">
          <AlertTitle>Couldn't refresh</AlertTitle>
          <AlertDescription>
            <p>{error}</p>
            <Button type="button" size="xs" variant="outline" onClick={props.onRetry}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {entries.length === 0 ? (
        <EnvNoEntries
          target={overview.target}
          isAdmin={isAdmin}
          busy={props.importBusy}
          error={props.importError}
          result={props.importResult}
          onImport={() => props.onImport(false)}
        />
      ) : (
        <>
          <EnvValidationPanel
            validation={overview.last_validation}
            missingKeys={missing}
            requiredCount={requiredCount}
            isAdmin={isAdmin && !readOnly}
            onAddKey={props.onAddKey}
          />

          {props.importResult !== null && (
            <p className="text-muted-foreground text-xs" role="status">
              Imported {props.importResult.imported}, skipped {props.importResult.skipped}
              {props.importResult.secret_guessed > 0 ? `, ${props.importResult.secret_guessed} marked secret by name` : ''}.
            </p>
          )}

          <Card className="overflow-hidden py-0">
            <div className="border-border flex flex-wrap items-center gap-2 border-b px-4 py-3">
              <div className="relative min-w-56 flex-1">
                <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2" aria-hidden />
                <Input
                  ref={props.searchRef}
                  value={filters.query}
                  placeholder="Search keys  ( / )"
                  aria-label="Search keys"
                  autoComplete="off"
                  spellCheck={false}
                  className="pl-8 font-mono text-xs"
                  onChange={(event) => props.onFilters({ ...filters, query: event.target.value })}
                  onKeyDown={(event) => {
                    if (event.key === 'Escape') {
                      props.onFilters({ ...filters, query: '' });
                      event.currentTarget.blur();
                    }
                  }}
                />
              </div>
              <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Filters">
                <FilterChip
                  label="Pending"
                  count={counts.total}
                  active={filters.pendingOnly}
                  onClick={() => props.onFilters({ ...filters, pendingOnly: !filters.pendingOnly })}
                />
                <FilterChip
                  label="Secrets"
                  count={secretCount}
                  active={filters.secretsOnly}
                  onClick={() => props.onFilters({ ...filters, secretsOnly: !filters.secretsOnly })}
                />
                <FilterChip
                  label="Required"
                  count={requiredCount}
                  active={filters.requiredOnly}
                  onClick={() => props.onFilters({ ...filters, requiredOnly: !filters.requiredOnly })}
                />
                {anyFilter && (
                  <Button type="button" size="xs" variant="ghost" onClick={() => props.onFilters(defaultEnvFilters)}>
                    Clear
                  </Button>
                )}
              </div>
              <Select value={sort} onValueChange={(value) => props.onSort(value as EnvSort)}>
                <SelectTrigger size="sm" aria-label="Sort entries" className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="key">Sort by key</SelectItem>
                  <SelectItem value="updated">Sort by last updated</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {isAdmin && (
              <EnvAddRow
                serviceId={service.id}
                disabled={readOnly}
                busy={props.addBusy}
                serverError={props.addError}
                prefillKey={props.prefillKey}
                existingKeys={existingKeys}
                onStage={props.onStage}
                onPaste={props.onPaste}
              />
            )}

            <CardContent className="px-0 py-0">
              <EnvEntriesTable
                entries={visible}
                isAdmin={isAdmin}
                readOnly={readOnly}
                busyKey={props.busyKey}
                reveal={props.reveal}
                now={props.now}
                sort={sort}
                onSort={props.onSort}
                actions={props.entryActions}
                emptyMessage={anyFilter ? 'No keys match these filters.' : 'No entries.'}
              />
            </CardContent>
            <div className="border-border text-muted-foreground flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2 text-xs">
              <span>
                {visible.length === entries.length
                  ? `${entries.length} ${entries.length === 1 ? 'entry' : 'entries'}`
                  : `${visible.length} of ${entries.length} entries`}
                {secretCount > 0 && ` · ${secretCount} secret`}
              </span>
              <span>Enter saves an edit · Esc cancels · / searches</span>
            </div>
          </Card>
        </>
      )}

      <EnvHelp target={overview.target} />

      <EnvPendingBar
        counts={counts}
        isAdmin={isAdmin}
        disabled={applyInProgress || props.busyKey !== null}
        onReview={props.onReview}
        onDiscard={props.onDiscard}
      />
    </div>
  );
}

export default function EnvironmentTab({
  service,
  isAdmin,
  onUnauthorized,
}: {
  service: ServiceView;
  isAdmin: boolean;
  onUnauthorized: () => void;
}) {
  const [overview, setOverview] = useState<EnvOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filters, setFilters] = useState<EnvFilters>(defaultEnvFilters);
  const [sort, setSort] = useState<EnvSort>('key');
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [addBusy, setAddBusy] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);
  const [prefillKey, setPrefillKey] = useState<string | null>(null);
  const [pasteText, setPasteText] = useState<string | null>(null);
  const [pasteBusy, setPasteBusy] = useState(false);
  const [pasteError, setPasteError] = useState<string | null>(null);
  const [reviewOpen, setReviewOpen] = useState(false);
  const [diff, setDiff] = useState<EnvDiff | null>(null);
  const [diffError, setDiffError] = useState<string | null>(null);
  const [applyBusy, setApplyBusy] = useState(false);
  const [applyError, setApplyError] = useState<string | null>(null);
  const [discardOpen, setDiscardOpen] = useState(false);
  const [discardBusy, setDiscardBusy] = useState(false);
  const [revisionsOpen, setRevisionsOpen] = useState(false);
  const [revisions, setRevisions] = useState<EnvRevision[] | null>(null);
  const [revisionsError, setRevisionsError] = useState<string | null>(null);
  const [rollbackBusy, setRollbackBusy] = useState(false);
  const [rollbackError, setRollbackError] = useState<string | null>(null);
  const [importBusy, setImportBusy] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [importResult, setImportResult] = useState<EnvImportResult | null>(null);
  const [replaceOpen, setReplaceOpen] = useState(false);
  const [runKind, setRunKind] = useState<'apply' | 'rollback'>('apply');
  const [viewerNotice, setViewerNotice] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [reveal, dispatchReveal] = useReducer(revealReducer, initialRevealState);
  const searchRef = useRef<HTMLInputElement | null>(null);

  const load = useCallback(async () => {
    setLoading(true);

    try {
      const fetched = await fetchEnv(service.id);
      setOverview(fetched);
      setError(null);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }
      if (err instanceof ApiError && (err.status === 503 || err.code === 'env_key_missing')) {
        setOverview({
          configured: false,
          reason: 'key_missing',
          target: null,
          entries: [],
          applied_revision: null,
          pending_count: 0,
          last_validation: null,
          apply_in_progress: false,
        });
        setError(null);
        return;
      }

      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [service.id, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleSettled = useCallback(
    (run: ApplyRunState) => {
      void load();

      if (run.status === 'done') {
        toast.success(runKind === 'apply' ? `Applied to ${service.name}` : `Rolled ${service.name} back`);
      }
    },
    [load, runKind, service.name],
  );

  const runHandle = useEnvApplyRun({ serviceId: service.id, onSettled: handleSettled, onUnauthorized });
  const { run, start: startRun } = runHandle;
  const runRunning = run !== null && run.status === 'running';

  // A run started elsewhere: follow it when the API names it, otherwise poll
  // the overview until the flag clears.
  useEffect(() => {
    if (overview === null || !overview.apply_in_progress || runRunning) {
      return;
    }

    const activeId = overview.active_run_id;

    if (typeof activeId === 'string' && activeId !== '') {
      setRunKind('apply');
      startRun(activeId);
      return;
    }

    const timer = setInterval(() => void load(), inProgressPollMs);

    return () => clearInterval(timer);
  }, [overview, runRunning, startRun, load]);

  // One-second clock for reveal countdowns and the elapsed timer.
  const needsClock = runRunning || reveal.expiresAt !== null || reveal.rateLimitedUntil !== null;

  useEffect(() => {
    if (!needsClock) {
      return;
    }

    const timer = setInterval(() => {
      const current = Date.now();
      setNow(current);
      dispatchReveal({ type: 'tick', now: current });
    }, 1000);

    return () => clearInterval(timer);
  }, [needsClock]);

  // `/` focuses search.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const action = envShortcut({
        key: event.key,
        metaKey: event.metaKey,
        ctrlKey: event.ctrlKey,
        altKey: event.altKey,
        targetTag: target?.tagName,
        targetEditable: target?.isContentEditable,
      });

      if (action === 'focus-search' && searchRef.current !== null) {
        event.preventDefault();
        searchRef.current.focus();
      }
    };

    document.addEventListener('keydown', onKey);

    return () => document.removeEventListener('keydown', onKey);
  }, []);

  // Drop any revealed value when the service changes or the tab unmounts.
  useEffect(() => () => dispatchReveal({ type: 'hide' }), [service.id]);

  // handleFailure maps API errors to the right surface. Returns true when it
  // handled the error (the caller then skips its own message).
  const handleFailure = useCallback(
    (err: unknown): boolean => {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return true;
      }
      if (err instanceof ApiError && err.status === 403) {
        setViewerNotice(true);
        return true;
      }

      const code = apiErrorCode(err);

      if (code === 'env_apply_in_progress') {
        toast.message('An apply is already running; showing it.');
        void load();
        return true;
      }
      if (code === 'env_key_missing' || (err instanceof ApiError && err.status === 503)) {
        void load();
        return true;
      }

      return false;
    },
    [onUnauthorized, load],
  );

  async function withKey(key: string, action: () => Promise<unknown>): Promise<void> {
    if (busyKey !== null) {
      return;
    }

    setBusyKey(key);

    try {
      await action();
      await load();
    } catch (err) {
      if (!handleFailure(err)) {
        toast.error(errorMessage(err));
      }
    } finally {
      setBusyKey(null);
    }
  }

  async function handleStage(key: string, value: string, isSecret: boolean): Promise<void> {
    if (addBusy) {
      return;
    }

    setAddBusy(true);
    setAddError(null);

    try {
      await putEnvEntry(service.id, key, { value, is_secret: isSecret });
      setPrefillKey(null);
      await load();
    } catch (err) {
      if (!handleFailure(err)) {
        setAddError(errorMessage(err));
      }
    } finally {
      setAddBusy(false);
    }
  }

  async function handlePasteStage(lines: StagedPasteLine[]): Promise<void> {
    if (pasteBusy) {
      return;
    }

    setPasteBusy(true);
    setPasteError(null);
    const failed: string[] = [];

    for (const line of lines) {
      try {
        await putEnvEntry(service.id, line.key, { value: line.value, is_secret: line.isSecret });
      } catch (err) {
        if (handleFailure(err)) {
          setPasteBusy(false);
          setPasteText(null);
          return;
        }
        failed.push(`${line.key}: ${errorMessage(err)}`);
      }
    }

    await load();
    setPasteBusy(false);

    if (failed.length > 0) {
      setPasteError(`${lines.length - failed.length} staged. Not staged — ${failed.join('; ')}`);
      return;
    }

    setPasteText(null);
    toast.success(`Staged ${lines.length} ${lines.length === 1 ? 'entry' : 'entries'}`);
  }

  async function revealValue(key: string): Promise<string | null> {
    dispatchReveal({ type: 'start', key });

    try {
      const { value } = await revealEnvEntry(service.id, key);
      return value;
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        dispatchReveal({ type: 'rate_limited', retryAfterSeconds: err.retryAfterSeconds, now: Date.now() });
        return null;
      }
      if (!handleFailure(err)) {
        toast.error(errorMessage(err));
      }
      dispatchReveal({ type: 'failed', message: errorMessage(err) });
      return null;
    }
  }

  async function handleReveal(key: string): Promise<void> {
    if (reveal.busyKey !== null) {
      return;
    }

    const value = await revealValue(key);

    if (value !== null) {
      const current = Date.now();
      setNow(current);
      dispatchReveal({ type: 'revealed', key, value, now: current });
    }
  }

  async function handleCopy(key: string): Promise<void> {
    if (reveal.busyKey !== null) {
      return;
    }

    const value = await revealValue(key);

    if (value === null) {
      return;
    }

    dispatchReveal({ type: 'failed', message: '' });

    try {
      await navigator.clipboard.writeText(value);
      toast.success(`Copied ${key}`);
    } catch {
      toast.error('The browser blocked clipboard access. Reveal the value and copy it by hand.');
    }
  }

  async function handleToggleSecret(entry: EnvEntry): Promise<void> {
    if (!entry.is_secret) {
      await withKey(entry.key, () =>
        putEnvEntry(service.id, entry.key, { value: entry.value ?? '', is_secret: true }),
      );
      return;
    }

    await withKey(entry.key, async () => {
      const { value } = await revealEnvEntry(service.id, entry.key);
      await putEnvEntry(service.id, entry.key, { value, is_secret: false });
    });
  }

  async function handleUndo(entry: EnvEntry): Promise<void> {
    const plan = undoPlan(entry);

    if (plan === null || plan.kind === 'unavailable') {
      return;
    }

    await withKey(entry.key, () =>
      plan.kind === 'delete'
        ? deleteEnvEntry(service.id, entry.key)
        : putEnvEntry(service.id, entry.key, { value: plan.value, is_secret: plan.is_secret }),
    );
  }

  async function openReview(): Promise<void> {
    setReviewOpen(true);
    setDiff(null);
    setDiffError(null);
    setApplyError(null);

    try {
      setDiff(await fetchEnvDiff(service.id));
    } catch (err) {
      if (!handleFailure(err)) {
        setDiffError(errorMessage(err));
      }
    }
  }

  async function handleApply(input: { confirm: string; summary: string }): Promise<void> {
    if (applyBusy) {
      return;
    }

    setApplyBusy(true);
    setApplyError(null);

    try {
      const { run_id: runId } = await applyEnv(service.id, input);
      setRunKind('apply');
      startRun(runId);
      setReviewOpen(false);
      // Any revealed value becomes stale once a new file is rendered.
      dispatchReveal({ type: 'hide' });
    } catch (err) {
      const code = apiErrorCode(err);

      if (code === 'env_nothing_to_apply') {
        setReviewOpen(false);
        toast.message('Nothing to apply — the staged changes are already gone.');
        void load();
      } else if (code === 'env_apply_in_progress') {
        setReviewOpen(false);
        handleFailure(err);
      } else if (!handleFailure(err)) {
        setApplyError(errorMessage(err));
      }
    } finally {
      setApplyBusy(false);
    }
  }

  async function handleDiscard(): Promise<void> {
    if (discardBusy) {
      return;
    }

    setDiscardBusy(true);

    try {
      await discardEnvChanges(service.id);
      setDiscardOpen(false);
      toast.success('Discarded the staged changes');
      await load();
    } catch (err) {
      if (!handleFailure(err)) {
        toast.error(errorMessage(err));
      }
    } finally {
      setDiscardBusy(false);
    }
  }

  const loadRevisions = useCallback(async () => {
    setRevisionsError(null);

    try {
      setRevisions(await fetchEnvRevisions(service.id));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }
      setRevisionsError(errorMessage(err));
    }
  }, [service.id, onUnauthorized]);

  function openRevisions(): void {
    setRevisionsOpen(true);
    setRevisions(null);
    setRollbackError(null);
    void loadRevisions();
  }

  async function handleRollback(revisionId: string, confirm: string): Promise<void> {
    if (rollbackBusy) {
      return;
    }

    setRollbackBusy(true);
    setRollbackError(null);

    try {
      const { run_id: runId } = await rollbackEnvRevision(service.id, revisionId, confirm);
      setRunKind('rollback');
      startRun(runId);
      setRevisionsOpen(false);
      dispatchReveal({ type: 'hide' });
    } catch (err) {
      if (apiErrorCode(err) === 'env_apply_in_progress') {
        setRevisionsOpen(false);
        handleFailure(err);
      } else if (!handleFailure(err)) {
        setRollbackError(errorMessage(err));
      }
    } finally {
      setRollbackBusy(false);
    }
  }

  async function handleImport(replace: boolean): Promise<void> {
    if (importBusy) {
      return;
    }
    if (replace && !replaceOpen) {
      setReplaceOpen(true);
      return;
    }

    setImportBusy(true);
    setImportError(null);
    setImportResult(null);

    try {
      const result = await importEnvFile(service.id, replace);
      setImportResult(result);
      setReplaceOpen(false);
      await load();
    } catch (err) {
      if (apiErrorCode(err) === 'env_has_entries') {
        setImportError('Entries already exist. Use "Re-import from the host file" to replace them.');
      } else if (!handleFailure(err)) {
        setImportError(errorMessage(err));
      }
    } finally {
      setImportBusy(false);
    }
  }

  const entryActions: EnvEntryActions = {
    onSave: (key, value, isSecret) =>
      void withKey(key, () => putEnvEntry(service.id, key, { value, is_secret: isSecret })),
    onRemove: (key) => void withKey(key, () => deleteEnvEntry(service.id, key)),
    onUndo: (entry) => void handleUndo(entry),
    onToggleSecret: (entry) => void handleToggleSecret(entry),
    onReveal: (key) => void handleReveal(key),
    onHideReveal: () => dispatchReveal({ type: 'hide' }),
    onCopy: (key) => void handleCopy(key),
  };

  const existingKeys = new Set((overview?.entries ?? []).map((entry) => entry.key));
  const pending = pendingCounts(overview?.entries ?? []);

  return (
    <>
      <EnvironmentTabView
        service={service}
        isAdmin={isAdmin}
        overview={overview}
        loading={loading}
        error={error}
        run={run}
        runKind={runKind}
        streamLost={runHandle.streamLost}
        now={now}
        filters={filters}
        sort={sort}
        busyKey={busyKey}
        addBusy={addBusy}
        addError={addError}
        prefillKey={prefillKey}
        importBusy={importBusy}
        importError={importError}
        importResult={importResult}
        reveal={reveal}
        viewerNotice={viewerNotice}
        searchRef={searchRef}
        onRetry={() => void load()}
        onFilters={setFilters}
        onSort={setSort}
        onStage={(key, value, isSecret) => void handleStage(key, value, isSecret)}
        onPaste={(text) => {
          setPasteError(null);
          setPasteText(text);
        }}
        onAddKey={(key) => {
          setPrefillKey(null);
          // Reset-then-set so the same key can be picked twice in a row.
          setTimeout(() => setPrefillKey(key), 0);
        }}
        onImport={(replace) => void handleImport(replace)}
        onOpenRevisions={openRevisions}
        onReview={() => void openReview()}
        onDiscard={() => setDiscardOpen(true)}
        onRetryRun={() => {
          runHandle.dismiss();
          void openReview();
        }}
        onDismissRun={runHandle.dismiss}
        entryActions={entryActions}
      />

      <EnvPasteSheet
        text={pasteText}
        existingKeys={existingKeys}
        busy={pasteBusy}
        error={pasteError}
        onClose={() => setPasteText(null)}
        onStage={(lines) => void handlePasteStage(lines)}
      />

      <EnvReviewDialog
        open={reviewOpen}
        serviceName={service.name}
        entries={overview?.entries ?? []}
        diff={diff}
        diffError={diffError}
        busy={applyBusy}
        error={applyError}
        onClose={() => setReviewOpen(false)}
        onApply={(input) => void handleApply(input)}
      />

      <EnvRevisionsSheet
        open={revisionsOpen}
        serviceName={service.name}
        isAdmin={isAdmin}
        disabled={runRunning || (overview?.apply_in_progress ?? false)}
        revisions={revisions}
        error={revisionsError}
        busy={rollbackBusy}
        rollbackError={rollbackError}
        onClose={() => setRevisionsOpen(false)}
        onRetry={() => void loadRevisions()}
        onRollback={(id, confirm) => void handleRollback(id, confirm)}
      />

      <Dialog open={discardOpen} onOpenChange={(open) => { if (!open && !discardBusy) setDiscardOpen(false); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Discard {pending.total === 1 ? 'the staged change' : `${pending.total} staged changes`}?</DialogTitle>
            <DialogDescription>
              The entries go back to what is applied on the host. Nothing on the host changes.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={discardBusy} onClick={() => setDiscardOpen(false)}>
              Keep them
            </Button>
            <Button type="button" variant="destructive" disabled={discardBusy} onClick={() => void handleDiscard()}>
              {discardBusy ? 'Discarding…' : 'Discard changes'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={replaceOpen} onOpenChange={(open) => { if (!open && !importBusy) setReplaceOpen(false); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Replace all entries with the host file?</DialogTitle>
            <DialogDescription>
              Every tracked entry, including staged changes, is replaced by what the file on the host
              holds right now. Secret-looking keys are marked secret by name.
            </DialogDescription>
          </DialogHeader>
          {importError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {importError}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={importBusy} onClick={() => setReplaceOpen(false)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={importBusy} onClick={() => void handleImport(true)}>
              {importBusy ? 'Importing…' : 'Replace entries'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
