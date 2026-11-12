import { useState, type KeyboardEvent } from 'react';
import { ChevronDown, ChevronUp, Lock, LockOpen, Pencil, Trash2, Undo2 } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import EnvSecretValue from './EnvSecretValue.tsx';
import EnvStateChip, { gutterClass } from './EnvStateChip.tsx';
import type { EnvEntry } from '@/lib/api.ts';
import {
  entryLockReason,
  longValueThreshold,
  secondsLeft,
  truncateValue,
  undoPlan,
  validateEnvValue,
  type EnvAccess,
  type EnvSort,
  type RevealState,
} from '@/lib/env.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

export interface EnvEntryActions {
  onSave: (key: string, value: string, isSecret: boolean) => void;
  onRemove: (key: string) => void;
  onUndo: (entry: EnvEntry) => void;
  onToggleSecret: (entry: EnvEntry) => void;
  onReveal: (key: string) => void;
  onHideReveal: () => void;
  onCopy: (key: string) => void;
}

interface EditState {
  key: string;
  draft: string;
  error: string | null;
}

interface RowContext extends EnvEntryActions {
  access: EnvAccess;
  readOnly: boolean;
  busyKey: string | null;
  reveal: RevealState;
  now: number;
  editing: EditState | null;
  expandedKey: string | null;
  onStartEdit: (entry: EnvEntry) => void;
  onEditChange: (draft: string) => void;
  onCancelEdit: () => void;
  onCommitEdit: (entry: EnvEntry) => void;
  onToggleExpand: (key: string) => void;
}

function KeyCell({ entry }: { entry: EnvEntry }) {
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-1.5">
      <span
        className={`truncate font-mono text-xs font-medium ${entry.state === 'removed' ? 'text-muted-foreground line-through' : ''}`}
      >
        {entry.key}
      </span>
      {entry.required && (
        <Badge variant="outline" title="The validator requires this key">
          required
        </Badge>
      )}
      {entry.is_secret && <Lock className="text-muted-foreground size-3" aria-label="Secret" />}
    </div>
  );
}

function ValueCell({ entry, ctx }: { entry: EnvEntry; ctx: RowContext }) {
  const editing = ctx.editing !== null && ctx.editing.key === entry.key;
  const busy = ctx.busyKey === entry.key;

  if (editing && ctx.editing !== null) {
    const edit = ctx.editing;
    const handleKey = (event: KeyboardEvent<HTMLInputElement>) => {
      if (event.key === 'Enter') {
        event.preventDefault();
        ctx.onCommitEdit(entry);
      } else if (event.key === 'Escape') {
        event.preventDefault();
        ctx.onCancelEdit();
      }
    };

    return (
      <div className="space-y-1">
        <div className="flex items-center gap-1">
          <Input
            autoFocus
            type={entry.is_secret ? 'password' : 'text'}
            autoComplete="off"
            spellCheck={false}
            aria-label={`New value for ${entry.key}`}
            aria-invalid={edit.error !== null}
            value={edit.draft}
            disabled={busy}
            placeholder={entry.is_secret ? 'New secret value' : ''}
            className="font-mono text-xs"
            onChange={(event) => ctx.onEditChange(event.target.value)}
            onKeyDown={handleKey}
            // Typing replaces the old value; the caret can still be placed by clicking.
            onFocus={(event) => event.currentTarget.select()}
          />
          <Button type="button" size="sm" disabled={busy} onClick={() => ctx.onCommitEdit(entry)}>
            {busy ? 'Saving…' : 'Save'}
          </Button>
          <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={ctx.onCancelEdit}>
            Cancel
          </Button>
        </div>
        {edit.error !== null ? (
          <p role="alert" className="text-destructive text-xs">
            {edit.error}
          </p>
        ) : (
          <p className="text-muted-foreground text-xs">Enter saves, Esc cancels. Saving stages the change.</p>
        )}
      </div>
    );
  }

  if (entry.is_secret) {
    const revealed = ctx.reveal.key === entry.key ? ctx.reveal.value : null;

    return (
      <EnvSecretValue
        entryKey={entry.key}
        canReveal={ctx.access.reveal}
        revealedValue={revealed}
        secondsLeft={secondsLeft(ctx.reveal.expiresAt, ctx.now)}
        busy={ctx.reveal.busyKey === entry.key || busy}
        rateLimitSeconds={secondsLeft(ctx.reveal.rateLimitedUntil, ctx.now)}
        disabled={ctx.readOnly && !ctx.access.reveal}
        onReveal={() => ctx.onReveal(entry.key)}
        onHide={ctx.onHideReveal}
        onCopy={() => ctx.onCopy(entry.key)}
      />
    );
  }

  const value = entry.value ?? '';
  const long = value.length > longValueThreshold;
  const expanded = ctx.expandedKey === entry.key;

  if (value === '') {
    return <span className="text-muted-foreground text-xs italic">empty</span>;
  }

  const previous =
    entry.state === 'changed' && typeof entry.applied_value === 'string' && entry.applied_value !== ''
      ? entry.applied_value
      : null;

  return (
    <div className="flex min-w-0 items-start gap-1">
      {previous !== null && (
        <span
          className="text-muted-foreground max-w-[30%] shrink-0 truncate font-mono text-xs line-through"
          title={`Applied value: ${previous}`}
          aria-label={`Applied value ${previous}`}
        >
          {truncateValue(previous, 24)}
        </span>
      )}
      <span
        className={`min-w-0 font-mono text-xs ${expanded ? 'break-all whitespace-pre-wrap' : 'truncate'} ${entry.state === 'removed' ? 'text-muted-foreground line-through' : ''}`}
      >
        {expanded || !long ? value : truncateValue(value)}
      </span>
      {long && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={expanded ? `Collapse ${entry.key}` : `Expand ${entry.key} (${value.length} characters)`}
          onClick={() => ctx.onToggleExpand(entry.key)}
        >
          {expanded ? <ChevronUp aria-hidden /> : <ChevronDown aria-hidden />}
        </Button>
      )}
    </div>
  );
}

function UpdatedCell({ entry }: { entry: EnvEntry }) {
  return (
    <span className="text-muted-foreground text-xs" title={formatTime(entry.updated_at)}>
      {entry.updated_by !== '' && <span>{entry.updated_by} · </span>}
      <time dateTime={entry.updated_at}>{timeAgo(entry.updated_at) || formatTime(entry.updated_at)}</time>
    </span>
  );
}

// LockedCell stands in for the row actions when a proposer may not
// change the entry, with the reason in a tooltip.
function LockedCell({ entry, reason }: { entry: EnvEntry; reason: string }) {
  return (
    <div className="flex items-center justify-end">
      <Tooltip>
        <TooltipTrigger asChild>
          <span
            tabIndex={0}
            className="text-muted-foreground inline-flex items-center gap-1 text-xs"
            aria-label={`${entry.key} is not editable: ${reason}`}
          >
            <Lock className="size-3" aria-hidden />
            {entry.is_secret ? 'Admin only' : 'Not yours'}
          </span>
        </TooltipTrigger>
        <TooltipContent>{reason}</TooltipContent>
      </Tooltip>
    </div>
  );
}

function ActionsCell({ entry, ctx }: { entry: EnvEntry; ctx: RowContext }) {
  if (!ctx.access.stage) {
    return null;
  }

  const lock = entryLockReason(entry, ctx.access);

  if (lock !== null) {
    return <LockedCell entry={entry} reason={lock} />;
  }

  const busy = ctx.busyKey === entry.key;
  const disabled = ctx.readOnly || busy || (ctx.editing !== null && ctx.editing.key === entry.key);
  const undo = undoPlan(entry);
  const removed = entry.state === 'removed';

  return (
    <div className="flex items-center justify-end gap-0.5">
      {!removed && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={entry.is_secret ? `Set a new value for ${entry.key}` : `Edit ${entry.key}`}
          title={entry.is_secret ? 'Set a new value' : 'Edit value'}
          disabled={disabled}
          onClick={() => ctx.onStartEdit(entry)}
        >
          <Pencil aria-hidden />
        </Button>
      )}
      {!removed && ctx.access.apply && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={entry.is_secret ? `Mark ${entry.key} as plain text` : `Mark ${entry.key} as secret`}
          title={
            entry.is_secret
              ? 'Mark as plain text (reveals the value to this browser first)'
              : 'Mark as secret (masked from now on)'
          }
          disabled={disabled}
          onClick={() => ctx.onToggleSecret(entry)}
        >
          {entry.is_secret ? <LockOpen aria-hidden /> : <Lock aria-hidden />}
        </Button>
      )}
      {!removed && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={`Remove ${entry.key}`}
          title="Stage removal"
          disabled={disabled}
          onClick={() => ctx.onRemove(entry.key)}
        >
          <Trash2 aria-hidden />
        </Button>
      )}
      {undo !== null && undo.kind !== 'unavailable' && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={`Undo the staged change to ${entry.key}`}
          title="Undo this change"
          disabled={disabled}
          onClick={() => ctx.onUndo(entry)}
        >
          <Undo2 aria-hidden />
        </Button>
      )}
      {undo !== null && undo.kind === 'unavailable' && (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="inline-flex" tabIndex={0} aria-label={`Undo not available for ${entry.key}`}>
              <Button type="button" variant="ghost" size="icon-xs" disabled aria-hidden tabIndex={-1}>
                <Undo2 />
              </Button>
            </span>
          </TooltipTrigger>
          <TooltipContent>{undo.reason}</TooltipContent>
        </Tooltip>
      )}
    </div>
  );
}

function SortButton({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={`inline-flex items-center gap-1 text-left ${active ? 'text-foreground' : 'hover:text-foreground'}`}
      aria-sort={active ? 'ascending' : 'none'}
      aria-label={`Sort by ${label.toLowerCase()}`}
      onClick={onClick}
    >
      {label}
      {active && <ChevronDown className="size-3" aria-hidden />}
    </button>
  );
}

export default function EnvEntriesTable({
  entries,
  access,
  readOnly,
  busyKey,
  reveal,
  now,
  sort,
  onSort,
  actions,
  emptyMessage,
}: {
  entries: EnvEntry[];
  access: EnvAccess;
  readOnly: boolean;
  busyKey: string | null;
  reveal: RevealState;
  now: number;
  sort: EnvSort;
  onSort: (sort: EnvSort) => void;
  actions: EnvEntryActions;
  emptyMessage: string;
}) {
  const [editing, setEditing] = useState<EditState | null>(null);
  const [expandedKey, setExpandedKey] = useState<string | null>(null);

  const ctx: RowContext = {
    ...actions,
    access,
    readOnly,
    busyKey,
    reveal,
    now,
    editing,
    expandedKey,
    onStartEdit: (entry) =>
      setEditing({ key: entry.key, draft: entry.is_secret ? '' : (entry.value ?? ''), error: null }),
    onEditChange: (draft) =>
      setEditing((current) => (current === null ? current : { ...current, draft, error: null })),
    onCancelEdit: () => setEditing(null),
    onCommitEdit: (entry) => {
      if (editing === null) {
        return;
      }

      const error = validateEnvValue(editing.draft);

      if (error !== null) {
        setEditing({ ...editing, error });
        return;
      }
      if (!entry.is_secret && editing.draft === (entry.value ?? '')) {
        setEditing(null);
        return;
      }

      actions.onSave(entry.key, editing.draft, entry.is_secret);
      setEditing(null);
    },
    onToggleExpand: (key) => setExpandedKey((current) => (current === key ? null : key)),
  };

  if (entries.length === 0) {
    return <p className="text-muted-foreground px-4 py-10 text-center text-sm">{emptyMessage}</p>;
  }

  return (
    <>
      {/* Wide layout */}
      <div className="hidden lg:block">
        <Table aria-label="Environment entries">
          <TableHeader>
            <TableRow>
              <TableHead className="w-[28%]">
                <SortButton label="Key" active={sort === 'key'} onClick={() => onSort('key')} />
              </TableHead>
              <TableHead>Value</TableHead>
              <TableHead className="w-[18%]">
                <SortButton label="Updated" active={sort === 'updated'} onClick={() => onSort('updated')} />
              </TableHead>
              <TableHead className="w-[9%]">State</TableHead>
              {access.stage && (
                <TableHead className="w-[12%] text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.map((entry) => (
              <TableRow
                key={entry.key}
                data-key={entry.key}
                data-state={entry.state}
                className={`border-l-2 ${gutterClass[entry.state]} ${entry.state === 'removed' ? 'bg-red-500/[0.03]' : ''}`}
              >
                <TableCell className="align-top">
                  <KeyCell entry={entry} />
                </TableCell>
                <TableCell className="max-w-0 align-top">
                  <ValueCell entry={entry} ctx={ctx} />
                </TableCell>
                <TableCell className="align-top">
                  <UpdatedCell entry={entry} />
                </TableCell>
                <TableCell className="align-top">
                  <EnvStateChip state={entry.state} />
                </TableCell>
                {access.stage && (
                  <TableCell className="align-top">
                    <ActionsCell entry={entry} ctx={ctx} />
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Narrow layout: one card per entry */}
      <ul className="divide-border divide-y lg:hidden" aria-label="Environment entries">
        {entries.map((entry) => (
          <li
            key={entry.key}
            data-key={entry.key}
            data-state={entry.state}
            className={`space-y-2 border-l-2 px-4 py-3 ${gutterClass[entry.state]}`}
          >
            <div className="flex items-start justify-between gap-2">
              <KeyCell entry={entry} />
              <EnvStateChip state={entry.state} />
            </div>
            <ValueCell entry={entry} ctx={ctx} />
            <div className="flex items-center justify-between gap-2">
              <UpdatedCell entry={entry} />
              <ActionsCell entry={entry} ctx={ctx} />
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}
