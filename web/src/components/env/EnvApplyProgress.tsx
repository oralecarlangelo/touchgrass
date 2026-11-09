import { Check, CircleDashed, LoaderCircle, X } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  applyStepCopy,
  applyStepOrder,
  elapsedSeconds,
  type ApplyRunState,
  type StepStatus,
} from '@/lib/env.ts';
import { formatDuration } from '@/lib/format.ts';

function StepIcon({ status }: { status: StepStatus }) {
  switch (status) {
    case 'done':
      return <Check className="size-3.5 text-emerald-600 dark:text-emerald-400" aria-hidden />;
    case 'running':
      return <LoaderCircle className="size-3.5 animate-spin" aria-hidden />;
    case 'failed':
      return <X className="text-destructive size-3.5" aria-hidden />;
    default:
      return <CircleDashed className="text-muted-foreground/60 size-3.5" aria-hidden />;
  }
}

const statusWord: Record<StepStatus, string> = {
  pending: 'waiting',
  running: 'running',
  done: 'done',
  failed: 'failed',
};

// EnvApplyProgress is the live view of one apply or rollback run: a vertical
// stepper, the log lines, elapsed time and the outcome.
export default function EnvApplyProgress({
  run,
  kind,
  now,
  streamLost,
  onRetry,
  onDismiss,
}: {
  run: ApplyRunState;
  kind: 'apply' | 'rollback';
  now: number;
  streamLost: boolean;
  onRetry: (() => void) | null;
  onDismiss: () => void;
}) {
  const elapsed = elapsedSeconds(run.startedAt, run.finishedAt, now);
  const noun = kind === 'apply' ? 'Apply' : 'Rollback';
  const title =
    run.status === 'running'
      ? `${noun} running`
      : run.status === 'done'
        ? `${noun} finished`
        : `${noun} failed`;

  return (
    <Card
      aria-live="polite"
      aria-label={`${noun} progress`}
      data-run-status={run.status}
      className={run.status === 'failed' ? 'border-destructive/40' : run.status === 'done' ? 'border-emerald-500/40' : ''}
    >
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-2">
        <div>
          <CardTitle className="text-base">{title}</CardTitle>
          <CardDescription>
            {run.status === 'running'
              ? 'The table is read-only until this finishes.'
              : run.status === 'done'
                ? 'The service is running on the new file.'
                : 'Your changes stay staged; nothing else moved.'}
            {' · '}
            <span className="tabular-nums">{formatDuration(elapsed)}</span>
          </CardDescription>
        </div>
        {run.status !== 'running' && (
          <div className="flex gap-2">
            {run.status === 'failed' && onRetry !== null && (
              <Button type="button" size="sm" onClick={onRetry}>
                Retry
              </Button>
            )}
            <Button type="button" size="sm" variant="ghost" onClick={onDismiss}>
              Dismiss
            </Button>
          </div>
        )}
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-[14rem_minmax(0,1fr)]">
        <ol className="space-y-2" aria-label={`${noun} steps`}>
          {applyStepOrder.map((step) => {
            const status = run.steps[step];
            const failed = status === 'failed';

            return (
              <li
                key={step}
                data-step={step}
                data-status={status}
                className={`flex items-start gap-2 rounded-md px-2 py-1 text-sm ${failed ? 'bg-destructive/10' : ''}`}
              >
                <span className="mt-0.5 shrink-0">
                  <StepIcon status={status} />
                </span>
                <span className="min-w-0">
                  <span className={`font-medium ${status === 'pending' ? 'text-muted-foreground' : ''}`}>
                    {applyStepCopy[step].label}
                  </span>
                  <span className="text-muted-foreground"> · {statusWord[status]}</span>
                  <span className="sr-only">. {applyStepCopy[step].detail}</span>
                </span>
              </li>
            );
          })}
        </ol>

        <div className="space-y-3">
          {streamLost && run.status === 'running' && (
            <p className="text-muted-foreground text-xs">
              Live stream disconnected; following the run by polling instead.
            </p>
          )}

          {run.status === 'failed' && (
            <Alert variant="destructive">
              <AlertTitle>
                {run.failedStep !== null ? `${applyStepCopy[run.failedStep].label} failed` : 'The run failed'}
              </AlertTitle>
              <AlertDescription>
                {run.error !== null && !run.validatorLines.includes(run.error) && <p>{run.error}</p>}
                {run.failedStep === 'validate' && run.validatorLines.length > 0 && (
                  <p>The validator rejected the rendered file; its output is below.</p>
                )}
                {run.restored && <p>The previous file was restored; the service is back on it.</p>}
                {run.failedStep === 'validate' && <p>Nothing was written to the host.</p>}
              </AlertDescription>
            </Alert>
          )}

          {run.validatorLines.length > 0 && (
            <div>
              <p className="mb-1 text-xs font-medium">Validator output</p>
              <pre
                aria-label="Validator output"
                className="max-h-48 overflow-auto rounded-md bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-100 dark:bg-zinc-900"
              >
                {run.validatorLines.join('\n')}
              </pre>
            </div>
          )}

          <ul
            aria-label={`${noun} log`}
            className="max-h-56 space-y-1 overflow-y-auto rounded-md bg-zinc-950 p-3 font-mono text-xs text-zinc-100 dark:bg-zinc-900"
          >
            {run.lines.length === 0 ? (
              <li className="text-zinc-400">Waiting for the first event…</li>
            ) : (
              run.lines.map((line, index) => (
                <li key={`${index}-${line.at}`} className="whitespace-pre-wrap">
                  <span className="text-zinc-500">[{line.step}]</span> {line.message}
                </li>
              ))
            )}
          </ul>
        </div>
      </CardContent>
    </Card>
  );
}
