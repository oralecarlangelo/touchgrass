import { useCallback, useState } from 'react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { useOperationEvents } from '../hooks/useOperationEvents.ts';
import { startRollback, type ServiceView } from '../lib/api.ts';

export default function RollbackPanel({
  service,
  onFinished,
}: {
  service: ServiceView;
  onFinished: () => void;
}) {
  const [confirmed, setConfirmed] = useState(false);
  const [resolvedTarget, setResolvedTarget] = useState<string | null>(null);
  const [streaming, setStreaming] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);

  const handleStreamFinished = useCallback(() => {
    setStreaming(false);
    onFinished();
  }, [onFinished]);

  const handleStreamError = useCallback(() => {
    setStreaming(false);
  }, []);

  const { lines, streamError, clear } = useOperationEvents({
    serviceId: service.id,
    active: streaming,
    finishedType: 'rollback_finished',
    operationNoun: 'rollback',
    onFinished: handleStreamFinished,
    onStreamError: handleStreamError,
  });

  async function handleStart(): Promise<void> {
    setStartError(null);
    clear();
    setResolvedTarget(null);

    try {
      const response = await startRollback(service.id);
      setResolvedTarget(response.target);
      setConfirmed(false);
      setStreaming(true);
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    }
  }

  const isBlueGreen = service.strategy === 'bluegreen';

  return (
    <Card>
      <CardHeader>
        <CardTitle>Rollback</CardTitle>
        <CardDescription>
          {isBlueGreen
            ? `Live color: ${service.live_color === '' ? 'unknown' : service.live_color}. Rollback flips traffic to the opposite live color and verifies that target afterward. It does not retag images; use the image rollback procedure for a bad release.`
            : 'Rollback re-runs the previous image in place and verifies health afterward, so expect brief downtime — the public URL is probed during the run.'}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {startError !== null && (
          <Alert variant="destructive">
            <AlertTitle>Rollback rejected</AlertTitle>
            <AlertDescription>{startError}</AlertDescription>
          </Alert>
        )}

        {streamError !== null && (
          <Alert variant="destructive">
            <AlertTitle>Stream interrupted</AlertTitle>
            <AlertDescription>{streamError}</AlertDescription>
          </Alert>
        )}

        <div className="flex items-start gap-2">
          <Checkbox
            id={`rollback-confirm-${service.id}`}
            checked={confirmed}
            disabled={streaming}
            onCheckedChange={(checked) => setConfirmed(checked === true)}
          />
          <Label htmlFor={`rollback-confirm-${service.id}`} className="font-normal">
            {isBlueGreen
              ? `I confirm rolling ${service.name} back to the previous live color.`
              : `I confirm rolling ${service.name} back (brief downtime expected).`}
          </Label>
        </div>

        <div>
          <Button
            type="button"
            variant="destructive"
            onClick={() => void handleStart()}
            disabled={!confirmed || streaming}
            aria-label="Start rollback"
          >
            {streaming ? 'Rollback running…' : 'Start rollback'}
          </Button>
        </div>

        {resolvedTarget !== null && (
          <p className="text-muted-foreground text-sm">
            {isBlueGreen
              ? `Accepted: rolling ${service.name} back to ${resolvedTarget}.`
              : `Accepted: rolling ${service.name} back (${resolvedTarget}).`}
          </p>
        )}

        {lines.length > 0 && (
          <ul
            aria-label="rollback progress"
            aria-live="polite"
            className="max-h-56 space-y-1 overflow-y-auto rounded-md bg-zinc-950 p-3 font-mono text-xs text-zinc-100 dark:bg-zinc-900"
          >
            {lines.map((line, index) => (
              <li key={`${index}-${line}`}>{line}</li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
