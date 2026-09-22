import { useState } from 'react';

import { ChevronDownIcon, ChevronRightIcon } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import type { SdkLogAttribute, SdkLogEntry } from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';
import { levelBadge } from './LogLineRow.tsx';

export function shortHex(value: string, keep = 12): string {
  return value.length > keep ? `${value.slice(0, keep)}…` : value;
}

function formatValue(value: SdkLogAttribute['value']): string {
  return typeof value === 'string' ? value : String(value);
}

export default function SdkLogRow({
  entry,
  onTraceSelect,
}: {
  entry: SdkLogEntry;
  onTraceSelect?: (traceId: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const level = entry.level === '' ? 'info' : entry.level;
  const badge = levelBadge(level);
  const attrs = Object.entries(entry.attributes);

  return (
    <li className="px-3 py-1.5 font-mono text-xs">
      <div className="flex items-start gap-2">
        <span className="shrink-0 text-[11px]">{formatTime(entry.ts)}</span>
        <Badge variant={badge.variant} className={badge.className} title={`Level: ${level}`}>
          {level}
        </Badge>
        {entry.release !== '' && (
          <Badge
            variant="outline"
            className="max-w-40 shrink-0 truncate text-[10px]"
            title={`Release: ${entry.release}`}
          >
            {entry.release}
          </Badge>
        )}
        <span className="min-w-0 flex-1 break-words whitespace-pre-wrap">{entry.message}</span>
        {entry.trace_id !== '' &&
          (onTraceSelect === undefined ? (
            <Badge
              variant="outline"
              className="shrink-0 text-[10px]"
              title={`Trace: ${entry.trace_id}`}
            >
              trace:{shortHex(entry.trace_id)}
            </Badge>
          ) : (
            <Badge asChild variant="outline" className="shrink-0 text-[10px]">
              <button
                type="button"
                onClick={() => onTraceSelect(entry.trace_id)}
                title={`Filter by trace ${entry.trace_id}`}
                className="cursor-pointer hover:bg-muted"
              >
                trace:{shortHex(entry.trace_id)}
              </button>
            </Badge>
          ))}
        {entry.span_id !== '' && (
          <Badge variant="outline" className="shrink-0 text-[10px]" title={`Span: ${entry.span_id}`}>
            span:{shortHex(entry.span_id)}
          </Badge>
        )}
        {attrs.length > 0 && (
          <button
            type="button"
            onClick={() => setExpanded(!expanded)}
            aria-expanded={expanded}
            title={expanded ? 'Hide attributes' : 'Show attributes'}
            className="text-muted-foreground hover:text-foreground inline-flex shrink-0 cursor-pointer items-center gap-0.5 text-[11px]"
          >
            {expanded ? <ChevronDownIcon /> : <ChevronRightIcon />}
            {attrs.length} attr{attrs.length === 1 ? '' : 's'}
          </button>
        )}
      </div>
      {expanded && attrs.length > 0 && (
        <dl className="border-border bg-muted/40 mt-1.5 ml-2 space-y-1 rounded-md border px-3 py-2">
          {attrs.map(([key, attr]) => (
            <div key={key} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <dt className="text-muted-foreground min-w-0 break-all">{key}</dt>
              <dd className="min-w-0 flex-1 break-all">{formatValue(attr.value)}</dd>
              <Badge variant="secondary" className="shrink-0 text-[10px]" title="Attribute type">
                {attr.type}
              </Badge>
            </div>
          ))}
        </dl>
      )}
    </li>
  );
}
