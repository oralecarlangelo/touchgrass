import type { EnvEntryState } from '@/lib/api.ts';
import { stateLabel } from '@/lib/env.ts';

// The three staged states share one colour language everywhere the tab
// shows a change: the row gutter, the chip, the pending bar, the review diff.
const chipClass: Record<Exclude<EnvEntryState, 'applied'>, string> = {
  added: 'bg-emerald-500/10 text-emerald-700 ring-emerald-500/30 dark:text-emerald-300',
  changed: 'bg-amber-500/10 text-amber-700 ring-amber-500/30 dark:text-amber-300',
  removed: 'bg-red-500/10 text-red-700 ring-red-500/30 dark:text-red-300',
};

export const gutterClass: Record<EnvEntryState, string> = {
  applied: 'border-l-transparent',
  added: 'border-l-emerald-500',
  changed: 'border-l-amber-500',
  removed: 'border-l-red-500',
};

export const stateTextClass: Record<Exclude<EnvEntryState, 'applied'>, string> = {
  added: 'text-emerald-700 dark:text-emerald-300',
  changed: 'text-amber-700 dark:text-amber-300',
  removed: 'text-red-700 dark:text-red-300',
};

export default function EnvStateChip({ state }: { state: EnvEntryState }) {
  if (state === 'applied') {
    return null;
  }

  return (
    <span
      data-state={state}
      className={`inline-flex h-5 items-center rounded-full px-2 text-xs font-medium ring-1 ring-inset ${chipClass[state]}`}
    >
      {stateLabel(state)}
    </span>
  );
}
