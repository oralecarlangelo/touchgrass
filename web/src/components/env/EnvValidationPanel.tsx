import { CircleCheck, TriangleAlert } from 'lucide-react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import type { EnvValidation } from '@/lib/api.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

// EnvValidationPanel shows the last validator result and, when keys are
// missing, a callout with one-click "Add" shortcuts that prefill the key.
export default function EnvValidationPanel({
  validation,
  missingKeys,
  requiredCount,
  isAdmin,
  onAddKey,
}: {
  validation: EnvValidation | null;
  missingKeys: string[];
  requiredCount: number;
  isAdmin: boolean;
  onAddKey: (key: string) => void;
}) {
  return (
    <div className="space-y-3">
      {missingKeys.length > 0 && (
        <Alert>
          <TriangleAlert aria-hidden />
          <AlertTitle>
            {missingKeys.length === 1 ? '1 required key is missing' : `${missingKeys.length} required keys are missing`}
          </AlertTitle>
          <AlertDescription>
            <p>The validator needs these before an apply can pass.</p>
            <ul className="flex flex-wrap gap-2" aria-label="Missing required keys">
              {missingKeys.map((key) => (
                <li key={key} className="flex items-center gap-1">
                  <span className="font-mono text-xs">{key}</span>
                  {isAdmin && (
                    <Button
                      type="button"
                      size="xs"
                      variant="outline"
                      aria-label={`Add ${key}`}
                      onClick={() => onAddKey(key)}
                    >
                      Add
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}

      <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs" aria-label="Last validation">
        {validation === null ? (
          <span>Not validated yet — the first apply runs the validator.</span>
        ) : validation.ok ? (
          <span className="inline-flex items-center gap-1">
            <CircleCheck className="size-3.5 text-emerald-600 dark:text-emerald-400" aria-hidden />
            Validation passed{' '}
            <time dateTime={validation.at} title={formatTime(validation.at)}>
              {timeAgo(validation.at) || formatTime(validation.at)}
            </time>
          </span>
        ) : (
          <span className="inline-flex items-center gap-1">
            <TriangleAlert className="text-destructive size-3.5" aria-hidden />
            Validation failed{' '}
            <time dateTime={validation.at} title={formatTime(validation.at)}>
              {timeAgo(validation.at) || formatTime(validation.at)}
            </time>
          </span>
        )}
        {requiredCount > 0 && (
          <span>
            {requiredCount} {requiredCount === 1 ? 'key' : 'keys'} marked required
          </span>
        )}
      </div>

      {validation !== null && !validation.ok && validation.lines.length > 0 && (
        <pre
          aria-label="Validator output"
          className="max-h-40 overflow-auto rounded-md bg-zinc-950 p-3 font-mono text-xs leading-relaxed text-zinc-100 dark:bg-zinc-900"
        >
          {validation.lines.join('\n')}
        </pre>
      )}
    </div>
  );
}
