import { CircleAlert, CircleCheck } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { MigrationProbe, MigrationStatus } from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';
import { verdictLabels, verdictSummary, verdictTones, type Tone } from '@/lib/migrations.ts';

export function toneVariant(tone: Tone): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (tone) {
    case 'good':
      return 'default';
    case 'bad':
      return 'destructive';
    case 'warn':
      return 'outline';
    default:
      return 'secondary';
  }
}

function ProbeLine({ label, probe }: { label: string; probe: MigrationProbe }) {
  return (
    <div className="flex items-start gap-2 text-sm">
      {probe.ok ? (
        <CircleCheck className="mt-0.5 h-4 w-4 shrink-0 text-emerald-600" aria-hidden />
      ) : (
        <CircleAlert className="text-destructive mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      )}
      <div className="min-w-0">
        <p>
          <span className="font-medium">{label}</span>{' '}
          {probe.target !== '' && <span className="text-muted-foreground font-mono text-xs break-all">{probe.target}</span>}
        </p>
        {!probe.ok && (
          <p className="text-destructive text-xs break-words">
            {probe.error !== undefined && probe.error !== '' ? probe.error : 'Could not be read.'}
          </p>
        )}
      </div>
    </div>
  );
}

// MigrationStatusCard is the answer to "does the database match this
// build": the verdict, what it means, and where each side was read.
export default function MigrationStatusCard({
  status,
  canManage,
  busy,
  onRefresh,
  onDeploy,
  onSetup,
}: {
  status: MigrationStatus;
  canManage: boolean;
  // busy is true while a command runs for this service.
  busy: boolean;
  onRefresh: () => void;
  onDeploy: () => void;
  onSetup: () => void;
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle className="text-base">Schema migrations</CardTitle>
          <Badge variant={toneVariant(verdictTones[status.verdict])}>{verdictLabels[status.verdict]}</Badge>
          {status.tool !== undefined && <Badge variant="secondary">{status.tool}</Badge>}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={onRefresh}>
            Refresh
          </Button>
          {canManage && (
            <>
              <Button variant="outline" size="sm" onClick={onSetup}>
                Setup
              </Button>
              {/* A failed migration blocks every later one: the tool refuses
                  to deploy until it is resolved, so the button waits too. */}
              <Button
                size="sm"
                onClick={onDeploy}
                disabled={busy || status.counts.pending === 0 || status.counts.failed > 0}
              >
                Apply pending migrations
              </Button>
            </>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm">{verdictSummary(status)}</p>
        <div className="space-y-2">
          <ProbeLine label="Database" probe={status.history} />
          <ProbeLine label="Running build" probe={status.shipped} />
        </div>
        <p className="text-muted-foreground text-xs">Checked {formatTime(status.checked_at)}</p>
      </CardContent>
    </Card>
  );
}
