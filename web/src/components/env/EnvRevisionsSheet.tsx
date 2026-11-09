import { useEffect, useState } from 'react';
import { History } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from '../ErrorState.tsx';
import { stateTextClass } from './EnvStateChip.tsx';
import { confirmationMatches } from './EnvReviewDialog.tsx';
import type { EnvRevision } from '@/lib/api.ts';
import { shortSha } from '@/lib/env.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

// RevisionKeys prints "+2 added · KEY_A, KEY_B" for one change kind; the
// count stays muted when nothing of that kind happened.
function RevisionKeys({
  sign,
  label,
  keys,
  className,
}: {
  sign: string;
  label: string;
  keys: string[];
  className: string;
}) {
  const count = keys.length;

  return (
    <span className={count > 0 ? className : 'text-muted-foreground'} title={count > 0 ? keys.join(', ') : undefined}>
      {sign}
      {count} {label}
      {count > 0 && <span className="text-muted-foreground font-mono"> · {keys.join(', ')}</span>}
    </span>
  );
}

export function sortRevisions(revisions: EnvRevision[]): EnvRevision[] {
  return [...revisions].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
}

// EnvRevisionsList is the portal-free body: newest first, the applied one
// badged, each with a rollback gate that expands in place.
export function EnvRevisionsList({
  revisions,
  error,
  serviceName,
  isAdmin,
  disabled,
  busy,
  rollbackTarget,
  confirm,
  rollbackError,
  onRetry,
  onPickRollback,
  onConfirmChange,
  onRollback,
}: {
  revisions: EnvRevision[] | null;
  error: string | null;
  serviceName: string;
  isAdmin: boolean;
  disabled: boolean;
  busy: boolean;
  rollbackTarget: string | null;
  confirm: string;
  rollbackError: string | null;
  onRetry: () => void;
  onPickRollback: (id: string | null) => void;
  onConfirmChange: (value: string) => void;
  onRollback: (id: string) => void;
}) {
  if (error !== null) {
    return (
      <div className="px-4">
        <ErrorState title="Couldn't load revisions" message={error} onRetry={onRetry} />
      </div>
    );
  }
  if (revisions === null) {
    return (
      <div className="space-y-2 px-4" aria-label="Loading revisions">
        <Skeleton className="h-16" />
        <Skeleton className="h-16" />
        <Skeleton className="h-16" />
      </div>
    );
  }
  if (revisions.length === 0) {
    return (
      <div className="px-4 py-10 text-center">
        <History className="text-muted-foreground mx-auto mb-2 size-6" aria-hidden />
        <p className="text-sm font-medium">No revisions yet</p>
        <p className="text-muted-foreground mt-1 text-sm">
          Every apply saves one. Roll back to any of them from here.
        </p>
      </div>
    );
  }

  const ordered = sortRevisions(revisions);

  return (
    <ol className="divide-border divide-y overflow-y-auto px-4" aria-label="Revisions">
      {ordered.map((revision) => {
        const picked = rollbackTarget === revision.id;
        const ready = confirmationMatches(confirm, serviceName) && !busy;

        return (
          <li key={revision.id} className="space-y-2 py-3" data-revision={revision.id} data-applied={revision.applied}>
            <div className="flex items-start justify-between gap-2">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium">
                  {revision.summary !== '' ? revision.summary : 'No summary'}
                </p>
                <p className="text-muted-foreground text-xs" title={formatTime(revision.created_at)}>
                  {revision.created_by}
                  {' · '}
                  <time dateTime={revision.created_at}>{timeAgo(revision.created_at) || formatTime(revision.created_at)}</time>
                  {revision.rendered_sha256 !== '' && (
                    <>
                      {' · '}
                      <span className="font-mono">{shortSha(revision.rendered_sha256)}</span>
                    </>
                  )}
                </p>
              </div>
              {revision.applied && <Badge>current</Badge>}
            </div>
            <p className="flex flex-wrap gap-x-3 gap-y-1 text-xs">
              <RevisionKeys sign="+" label="added" keys={revision.added} className={stateTextClass.added} />
              <RevisionKeys sign="~" label="changed" keys={revision.changed} className={stateTextClass.changed} />
              <RevisionKeys sign="−" label="removed" keys={revision.removed} className={stateTextClass.removed} />
            </p>
            {isAdmin && !revision.applied && (
              picked ? (
                <div className="space-y-2 rounded-md border p-3">
                  <p className="text-sm">
                    Rolling back re-applies this revision's file: render, validate, write, restart, probe.
                    Staged changes stay staged.
                  </p>
                  <div className="space-y-1.5">
                    <Label htmlFor={`env-rollback-confirm-${revision.id}`}>
                      Type <span className="font-mono">{serviceName}</span> to confirm
                    </Label>
                    <Input
                      id={`env-rollback-confirm-${revision.id}`}
                      value={confirm}
                      disabled={busy}
                      autoComplete="off"
                      spellCheck={false}
                      className="font-mono"
                      onChange={(event) => onConfirmChange(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter' && ready) {
                          event.preventDefault();
                          onRollback(String(revision.id));
                        }
                      }}
                    />
                  </div>
                  {rollbackError !== null && (
                    <p role="alert" className="text-destructive text-xs">
                      {rollbackError}
                    </p>
                  )}
                  <div className="flex justify-end gap-2">
                    <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => onPickRollback(null)}>
                      Cancel
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="destructive"
                      disabled={!ready}
                      aria-label={`Roll back to revision ${revision.id}`}
                      onClick={() => onRollback(String(revision.id))}
                    >
                      {busy ? 'Starting…' : 'Roll back'}
                    </Button>
                  </div>
                </div>
              ) : (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={disabled || busy}
                  onClick={() => onPickRollback(String(revision.id))}
                >
                  Roll back to this
                </Button>
              )
            )}
          </li>
        );
      })}
    </ol>
  );
}

export default function EnvRevisionsSheet({
  open,
  serviceName,
  isAdmin,
  disabled,
  revisions,
  error,
  busy,
  rollbackError,
  onClose,
  onRetry,
  onRollback,
}: {
  open: boolean;
  serviceName: string;
  isAdmin: boolean;
  disabled: boolean;
  revisions: EnvRevision[] | null;
  error: string | null;
  busy: boolean;
  rollbackError: string | null;
  onClose: () => void;
  onRetry: () => void;
  onRollback: (revisionId: string, confirm: string) => void;
}) {
  const [target, setTarget] = useState<string | null>(null);
  const [confirm, setConfirm] = useState('');

  useEffect(() => {
    if (!open) {
      setTarget(null);
      setConfirm('');
    }
  }, [open]);

  return (
    <Sheet open={open} onOpenChange={(next) => { if (!next && !busy) onClose(); }}>
      <SheetContent side="right" className="sm:max-w-lg">
        <SheetHeader>
          <SheetTitle>Revisions</SheetTitle>
          <SheetDescription>
            Every applied environment file for {serviceName}, newest first.
          </SheetDescription>
        </SheetHeader>
        <EnvRevisionsList
          revisions={revisions}
          error={error}
          serviceName={serviceName}
          isAdmin={isAdmin}
          disabled={disabled}
          busy={busy}
          rollbackTarget={target}
          confirm={confirm}
          rollbackError={rollbackError}
          onRetry={onRetry}
          onPickRollback={(id) => {
            setTarget(id);
            setConfirm('');
          }}
          onConfirmChange={setConfirm}
          onRollback={(id) => onRollback(id, confirm.trim())}
        />
      </SheetContent>
    </Sheet>
  );
}
