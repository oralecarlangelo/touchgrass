import { Copy, Eye, EyeOff } from 'lucide-react';

import { Button } from '@/components/ui/button';
import { maskedValue } from '@/lib/env.ts';

// EnvSecretValue renders a secret cell. The value only exists in the DOM
// while `revealedValue` is set; viewers never get an eye or a copy button.
export default function EnvSecretValue({
  entryKey,
  canReveal,
  revealedValue,
  secondsLeft,
  busy,
  rateLimitSeconds,
  disabled,
  onReveal,
  onHide,
  onCopy,
}: {
  entryKey: string;
  canReveal: boolean;
  revealedValue: string | null;
  secondsLeft: number;
  busy: boolean;
  rateLimitSeconds: number;
  disabled: boolean;
  onReveal: () => void;
  onHide: () => void;
  onCopy: () => void;
}) {
  const revealed = revealedValue !== null;
  const rateLimited = rateLimitSeconds > 0;

  return (
    <div className="flex min-w-0 items-center gap-1">
      {revealed ? (
        <span className="min-w-0 truncate font-mono text-xs" data-revealed="true">
          {revealedValue}
        </span>
      ) : (
        <span className="text-muted-foreground font-mono text-xs tracking-wider select-none" aria-label="Secret value hidden">
          {maskedValue}
        </span>
      )}

      {canReveal && (
        <>
          {revealed ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={`Hide ${entryKey} (${secondsLeft}s left)`}
              title={`Hides in ${secondsLeft}s`}
              onClick={onHide}
            >
              <EyeOff aria-hidden />
            </Button>
          ) : (
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label={rateLimited ? `Reveal ${entryKey} (wait ${rateLimitSeconds}s)` : `Reveal ${entryKey}`}
              title={rateLimited ? `Too many reveals; try again in ${rateLimitSeconds}s` : 'Show for 20 seconds'}
              disabled={disabled || busy || rateLimited}
              onClick={onReveal}
            >
              <Eye aria-hidden />
            </Button>
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`Copy ${entryKey} to clipboard`}
            title="Copy without showing"
            disabled={disabled || busy || rateLimited}
            onClick={onCopy}
          >
            <Copy aria-hidden />
          </Button>
          {revealed && (
            <span className="text-muted-foreground tabular-nums text-[11px]" aria-live="polite">
              {secondsLeft}s
            </span>
          )}
          {rateLimited && !revealed && (
            <span className="text-muted-foreground tabular-nums text-[11px]" aria-live="polite">
              wait {rateLimitSeconds}s
            </span>
          )}
        </>
      )}
    </div>
  );
}
