import { Button } from '@/components/ui/button';
import { stateTextClass } from './EnvStateChip.tsx';
import { describeAuthors, describePending, type PendingAuthor, type PendingCounts } from '@/lib/env.ts';

// EnvPendingBar is the sticky footer that appears while staged changes
// exist. It reads the same colours as the row gutters, names who staged
// the changes, and offers Apply only to a user who may apply: everyone
// else sees that the changes wait for an admin.
export default function EnvPendingBar({
  counts,
  authors,
  canApply,
  canDiscard,
  disabled,
  onReview,
  onDiscard,
}: {
  counts: PendingCounts;
  authors: PendingAuthor[];
  canApply: boolean;
  canDiscard: boolean;
  disabled: boolean;
  onReview: () => void;
  onDiscard: () => void;
}) {
  if (counts.total === 0) {
    return null;
  }

  const parts: { label: string; value: number; state: 'added' | 'changed' | 'removed' }[] = [
    { label: 'added', value: counts.added, state: 'added' },
    { label: 'changed', value: counts.changed, state: 'changed' },
    { label: 'removed', value: counts.removed, state: 'removed' },
  ];

  return (
    <div
      role="status"
      aria-label={describePending(counts)}
      className="border-border bg-card/95 supports-backdrop-filter:bg-card/80 sticky bottom-0 z-30 -mx-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t px-4 py-3 backdrop-blur sm:mx-0 sm:rounded-lg sm:border"
    >
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm">
        <span className="font-medium">
          {counts.total === 1 ? '1 change not applied' : `${counts.total} changes not applied`}
        </span>
        {parts
          .filter((part) => part.value > 0)
          .map((part) => (
            <span key={part.state} className={`text-xs ${stateTextClass[part.state]}`}>
              {part.value} {part.label}
            </span>
          ))}
        {authors.length > 0 && (
          <span className="text-muted-foreground text-xs">Staged by {describeAuthors(authors)}</span>
        )}
      </div>
      {canApply ? (
        <div className="ml-auto flex gap-2">
          {canDiscard && (
            <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={onDiscard}>
              Discard
            </Button>
          )}
          <Button type="button" size="sm" disabled={disabled} onClick={onReview}>
            Review &amp; apply
          </Button>
        </div>
      ) : (
        <span className="text-muted-foreground ml-auto text-xs font-medium">Awaiting an admin</span>
      )}
    </div>
  );
}
