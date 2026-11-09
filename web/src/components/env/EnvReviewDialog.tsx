import { useEffect, useState } from 'react';
import { TriangleAlert } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
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
import { stateTextClass } from './EnvStateChip.tsx';
import type { EnvDiff, EnvEntry } from '@/lib/api.ts';
import { applyStepCopy, applyStepOrder, shortSha, suggestSummary } from '@/lib/env.ts';

export function confirmationMatches(input: string, serviceName: string): boolean {
  return input.trim() === serviceName.trim() && serviceName.trim() !== '';
}

function DiffColumn({
  title,
  keys,
  state,
}: {
  title: string;
  keys: string[];
  state: 'added' | 'changed' | 'removed';
}) {
  return (
    <div className="min-w-0">
      <p className={`text-xs font-medium ${stateTextClass[state]}`}>
        {title} <span className="text-muted-foreground font-normal">({keys.length})</span>
      </p>
      {keys.length === 0 ? (
        <p className="text-muted-foreground mt-1 text-xs">none</p>
      ) : (
        <ul className="mt-1 space-y-0.5" aria-label={`${title} keys`}>
          {keys.map((key) => (
            <li
              key={key}
              className={`truncate font-mono text-xs ${state === 'removed' ? 'text-muted-foreground line-through' : ''}`}
            >
              {key}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// EnvReviewBody is the portal-free body of the review dialog: the diff, the
// summary, the typed-name gate and the pipeline the apply will run.
export function EnvReviewBody({
  serviceName,
  diff,
  diffError,
  summary,
  confirm,
  onSummaryChange,
  onConfirmChange,
  onSubmitAttempt,
  busy,
  error,
}: {
  serviceName: string;
  diff: EnvDiff | null;
  diffError: string | null;
  summary: string;
  confirm: string;
  onSummaryChange: (value: string) => void;
  onConfirmChange: (value: string) => void;
  onSubmitAttempt: () => void;
  busy: boolean;
  error: string | null;
}) {
  const ready = confirmationMatches(confirm, serviceName);

  return (
    <div className="space-y-4">
      {diffError !== null ? (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>Couldn't load the diff</AlertTitle>
          <AlertDescription>{diffError}</AlertDescription>
        </Alert>
      ) : diff === null ? (
        <div className="grid gap-3 sm:grid-cols-3" aria-label="Loading diff">
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </div>
      ) : (
        <>
          <div className="grid gap-3 rounded-lg border p-3 sm:grid-cols-3">
            <DiffColumn title="Added" keys={diff.added} state="added" />
            <DiffColumn title="Changed" keys={diff.changed} state="changed" />
            <DiffColumn title="Removed" keys={diff.removed} state="removed" />
          </div>
          {diff.rendered_sha256 !== '' && (
            <p className="text-muted-foreground text-xs">
              Rendered file <span className="font-mono">{shortSha(diff.rendered_sha256)}</span>
            </p>
          )}
        </>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="env-apply-summary">Summary (optional)</Label>
        <Input
          id="env-apply-summary"
          value={summary}
          disabled={busy}
          maxLength={200}
          placeholder="What changed and why"
          onChange={(event) => onSummaryChange(event.target.value)}
        />
        <p className="text-muted-foreground text-xs">Shown in Revisions and the audit log.</p>
      </div>

      <div className="space-y-1.5">
        <p className="text-sm font-medium">What will happen</p>
        <ol className="space-y-1" aria-label="Apply steps">
          {applyStepOrder.map((step, index) => (
            <li key={step} className="flex gap-2 text-sm">
              <span className="text-muted-foreground w-4 shrink-0 tabular-nums">{index + 1}.</span>
              <span>
                <span className="font-medium">{applyStepCopy[step].label}</span>
                <span className="text-muted-foreground"> — {applyStepCopy[step].detail}</span>
              </span>
            </li>
          ))}
        </ol>
        <p className="text-muted-foreground text-xs">
          Restart means a brief interruption for {serviceName}. If validation or the probe fails, the
          previous file is restored and your changes stay staged.
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="env-apply-confirm">
          Type <span className="font-mono">{serviceName}</span> to confirm
        </Label>
        <Input
          id="env-apply-confirm"
          value={confirm}
          disabled={busy}
          autoComplete="off"
          spellCheck={false}
          aria-describedby="env-apply-confirm-hint"
          className="font-mono"
          onChange={(event) => onConfirmChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && ready && !busy) {
              event.preventDefault();
              onSubmitAttempt();
            }
          }}
        />
        <p id="env-apply-confirm-hint" className="text-muted-foreground text-xs">
          Same gate as a cutover: the apply button stays off until the name matches.
        </p>
      </div>

      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
    </div>
  );
}

// EnvReviewFooter holds the gate: Apply stays disabled until the typed
// name matches and the diff has loaded.
export function EnvReviewFooter({
  ready,
  busy,
  onClose,
  onSubmit,
}: {
  ready: boolean;
  busy: boolean;
  onClose: () => void;
  onSubmit: () => void;
}) {
  return (
    <>
      <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
        Cancel
      </Button>
      <Button type="button" disabled={!ready} onClick={onSubmit} aria-label="Apply environment changes">
        {busy ? 'Starting…' : 'Apply'}
      </Button>
    </>
  );
}

export default function EnvReviewDialog({
  open,
  serviceName,
  entries,
  diff,
  diffError,
  busy,
  error,
  onClose,
  onApply,
}: {
  open: boolean;
  serviceName: string;
  entries: EnvEntry[];
  diff: EnvDiff | null;
  diffError: string | null;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onApply: (input: { confirm: string; summary: string }) => void;
}) {
  const [summary, setSummary] = useState('');
  const [summaryTouched, setSummaryTouched] = useState(false);
  const [confirm, setConfirm] = useState('');

  useEffect(() => {
    if (open) {
      setConfirm('');
      setSummaryTouched(false);
    }
  }, [open]);

  useEffect(() => {
    if (open && !summaryTouched && diff !== null) {
      setSummary(suggestSummary(diff, entries));
    }
  }, [open, diff, entries, summaryTouched]);

  const ready = confirmationMatches(confirm, serviceName) && diff !== null && !busy;
  const submit = () => {
    if (ready) {
      onApply({ confirm: confirm.trim(), summary: summary.trim() });
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onClose(); }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Review and apply to {serviceName}</DialogTitle>
          <DialogDescription>
            The staged entries below become the service's environment file on the host.
          </DialogDescription>
        </DialogHeader>
        <EnvReviewBody
          serviceName={serviceName}
          diff={diff}
          diffError={diffError}
          summary={summary}
          confirm={confirm}
          onSummaryChange={(value) => {
            setSummary(value);
            setSummaryTouched(true);
          }}
          onConfirmChange={setConfirm}
          onSubmitAttempt={submit}
          busy={busy}
          error={error}
        />
        <DialogFooter>
          <EnvReviewFooter ready={ready} busy={busy} onClose={onClose} onSubmit={submit} />
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
