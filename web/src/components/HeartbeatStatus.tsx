import type { ReactNode } from 'react';

import { Badge } from '@/components/ui/badge';
import { formatTime, timeAgo } from '@/lib/format.ts';
import type { HeartbeatState } from '@/lib/heartbeat.ts';

const labels: Record<HeartbeatState, string> = {
  waiting: 'Collecting', current: 'Current', degraded: 'Degraded', stale: 'Stale', unavailable: 'Unavailable',
};

export default function HeartbeatStatus({
  label, state, observedAt, timeLabel = 'Last successful sample', detail, action,
}: {
  label: string;
  state: HeartbeatState;
  observedAt?: string | null;
  timeLabel?: string;
  detail?: string;
  action?: ReactNode;
}) {
  return (
    <div className="space-y-1 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{label}</span>
        <Badge variant={state === 'unavailable' ? 'destructive' : 'outline'}>{labels[state]}</Badge>
      </div>
      {observedAt && (
        <p className="text-muted-foreground text-xs">
          {timeLabel} <time dateTime={observedAt} title={formatTime(observedAt)}>{timeAgo(observedAt)}</time>
        </p>
      )}
      {detail && <p className="text-muted-foreground text-xs">{detail}</p>}
      {action}
    </div>
  );
}
