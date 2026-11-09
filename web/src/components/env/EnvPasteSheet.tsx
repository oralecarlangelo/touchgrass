import { useEffect, useMemo, useState } from 'react';
import { Lock } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet';
import { parseDotenvLines, type ParsedEnvLine } from '@/lib/env.ts';

export interface PasteLineDecision {
  accept: boolean;
  secret: boolean;
}

export interface StagedPasteLine {
  key: string;
  value: string;
  isSecret: boolean;
}

export function defaultDecisions(lines: ParsedEnvLine[]): Record<number, PasteLineDecision> {
  const decisions: Record<number, PasteLineDecision> = {};

  for (const line of lines) {
    decisions[line.line] = { accept: line.status === 'ok', secret: line.secret };
  }

  return decisions;
}

export function acceptedLines(
  lines: ParsedEnvLine[],
  decisions: Record<number, PasteLineDecision>,
): StagedPasteLine[] {
  return lines
    .filter((line) => line.status === 'ok' && decisions[line.line]?.accept === true)
    .map((line) => ({
      key: line.key,
      value: line.value,
      isSecret: decisions[line.line]?.secret ?? line.secret,
    }));
}

// EnvPasteReview is the body of the sheet, kept separate so it renders
// without a portal: tests and the sheet both use it.
export function EnvPasteReview({
  lines,
  decisions,
  existingKeys,
  busy,
  onDecision,
}: {
  lines: ParsedEnvLine[];
  decisions: Record<number, PasteLineDecision>;
  existingKeys: Set<string>;
  busy: boolean;
  onDecision: (line: number, decision: Partial<PasteLineDecision>) => void;
}) {
  if (lines.length === 0) {
    return (
      <p className="text-muted-foreground px-4 text-sm">
        Nothing to stage: only blank lines and comments were pasted.
      </p>
    );
  }

  return (
    <ul className="divide-border divide-y overflow-y-auto px-4" aria-label="Pasted lines">
      {lines.map((line) => {
        const decision = decisions[line.line] ?? { accept: false, secret: line.secret };
        const usable = line.status === 'ok';
        const replaces = usable && existingKeys.has(line.key);

        return (
          <li key={line.line} className="flex items-start gap-3 py-2.5" data-line-status={line.status}>
            <Checkbox
              id={`paste-accept-${line.line}`}
              checked={usable && decision.accept}
              disabled={!usable || busy}
              aria-label={`Stage line ${line.line}${line.key !== '' ? ` (${line.key})` : ''}`}
              className="mt-0.5"
              onCheckedChange={(checked) => onDecision(line.line, { accept: checked === true })}
            />
            <div className="min-w-0 flex-1 space-y-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="text-muted-foreground w-6 shrink-0 text-right font-mono text-[11px]">
                  {line.line}
                </span>
                {usable ? (
                  <>
                    <span className="font-mono text-xs font-medium">{line.key}</span>
                    <span className="text-muted-foreground font-mono text-xs">=</span>
                    <span className="text-muted-foreground min-w-0 truncate font-mono text-xs">
                      {decision.secret ? '••••••••' : line.value === '' ? '(empty)' : line.value}
                    </span>
                  </>
                ) : (
                  <span className="text-muted-foreground min-w-0 truncate font-mono text-xs line-through">
                    {line.raw.trim()}
                  </span>
                )}
              </div>
              {line.message !== null && (
                <p className={`text-xs ${usable ? 'text-muted-foreground' : 'text-destructive'}`}>{line.message}</p>
              )}
              {replaces && decision.accept && (
                <p className="text-xs text-amber-700 dark:text-amber-300">Replaces the current {line.key}.</p>
              )}
            </div>
            {usable && (
              <label className="flex shrink-0 items-center gap-1.5 text-xs">
                <Checkbox
                  checked={decision.secret}
                  disabled={busy}
                  aria-label={`Treat ${line.key} as secret`}
                  onCheckedChange={(checked) => onDecision(line.line, { secret: checked === true })}
                />
                <Lock className="size-3" aria-hidden />
                Secret
              </label>
            )}
          </li>
        );
      })}
    </ul>
  );
}

export default function EnvPasteSheet({
  text,
  existingKeys,
  busy,
  error,
  onClose,
  onStage,
}: {
  text: string | null;
  existingKeys: Set<string>;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onStage: (lines: StagedPasteLine[]) => void;
}) {
  const lines = useMemo(() => (text === null ? [] : parseDotenvLines(text)), [text]);
  const [decisions, setDecisions] = useState<Record<number, PasteLineDecision>>({});

  useEffect(() => {
    setDecisions(defaultDecisions(lines));
  }, [lines]);

  const accepted = acceptedLines(lines, decisions);
  const skipped = lines.filter((line) => line.status !== 'ok' || decisions[line.line]?.accept !== true).length;

  return (
    <Sheet open={text !== null} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
      <SheetContent side="right" className="sm:max-w-lg" aria-describedby={undefined}>
        <SheetHeader>
          <SheetTitle>Paste .env lines</SheetTitle>
          <SheetDescription>
            Each accepted line is staged as one entry; nothing reaches the host until you review and apply.
            Secret-looking keys are masked by default.
          </SheetDescription>
        </SheetHeader>
        <EnvPasteReview
          lines={lines}
          decisions={decisions}
          existingKeys={existingKeys}
          busy={busy}
          onDecision={(line, decision) =>
            setDecisions((current) => ({
              ...current,
              [line]: { ...(current[line] ?? { accept: false, secret: false }), ...decision },
            }))
          }
        />
        <SheetFooter>
          {error !== null && (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          )}
          <p className="text-muted-foreground text-xs">
            {accepted.length} to stage{skipped > 0 ? ` · ${skipped} skipped` : ''}
          </p>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="button" disabled={busy || accepted.length === 0} onClick={() => onStage(accepted)}>
              {busy ? 'Staging…' : `Stage ${accepted.length} ${accepted.length === 1 ? 'entry' : 'entries'}`}
            </Button>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}
