import { Badge } from '@/components/ui/badge';
import type { LogLine } from '@/lib/api.ts';
import { formatTime, stripAnsi } from '@/lib/format.ts';

// Shared with the SDK log rows (S20 adds trace + fatal to the level set;
// container levels stay error/warn/info/debug).
export function levelBadge(level: string): {
  variant: 'destructive' | 'outline' | 'secondary';
  className: string;
} {
  switch (level) {
    case 'error':
    case 'fatal':
      return { variant: 'destructive', className: 'shrink-0 text-[10px] uppercase' };
    case 'warn':
      return {
        variant: 'outline',
        className:
          'shrink-0 border-amber-500/50 bg-amber-500/10 text-[10px] text-amber-700 uppercase dark:text-amber-400',
      };
    case 'debug':
    case 'trace':
      return {
        variant: 'outline',
        className: 'shrink-0 text-[10px] text-muted-foreground uppercase',
      };
    default:
      return { variant: 'secondary', className: 'shrink-0 text-[10px] uppercase' };
  }
}

export default function LogLineRow({
  line,
  anchor,
  onSelect,
}: {
  line: LogLine;
  anchor: boolean;
  onSelect?: (line: LogLine) => void;
}) {
  const level = line.level === '' ? 'info' : line.level;
  const badge = levelBadge(level);
  const content = (
    <>
      <span className="shrink-0 text-[11px]">{formatTime(line.ts)}</span>
      <Badge variant="outline" className="shrink-0 font-mono text-[10px]">
        {line.container}
      </Badge>
      <Badge variant={badge.variant} className={badge.className} title={`Level: ${level}`}>
        {level}
      </Badge>
      <Badge
        variant={line.stream === 'stderr' ? 'destructive' : 'secondary'}
        className="shrink-0 text-[10px]"
      >
        {line.stream}
      </Badge>
      <span className="min-w-0 flex-1 break-words whitespace-pre-wrap">
        {stripAnsi(line.line)}
      </span>
    </>
  );

  if (onSelect === undefined) {
    return (
      <li
        className={`flex items-start gap-2 px-3 py-1.5 font-mono text-xs ${
          anchor ? 'bg-accent' : ''
        }`}
      >
        {content}
      </li>
    );
  }

  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(line)}
        title="Show surrounding context"
        className="hover:bg-muted/60 flex w-full items-start gap-2 px-3 py-1.5 text-left font-mono text-xs"
      >
        {content}
      </button>
    </li>
  );
}
