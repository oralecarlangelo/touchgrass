import { useCallback, useState } from 'react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import { useOperationEvents } from '../hooks/useOperationEvents.ts';
import { startDeploy, type ServiceView } from '../lib/api.ts';

export default function DeployPanel({
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
    finishedType: 'deploy_finished',
    operationNoun: 'deploy',
    onFinished: handleStreamFinished,
    onStreamError: handleStreamError,
  });

  async function handleStart(): Promise<void> {
    setStartError(null);
    clear();
    setResolvedTarget(null);

    try {
      const response = await startDeploy(service.id);
      setResolvedTarget(response.target);
      setConfirmed(false);
      setStreaming(true);
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Deploy</CardTitle>
        <CardDescription>
          {service.name} recreates its container in place, so expect brief downtime — the public URL
          is probed during the run and the seconds land in deploy history.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {startError !== null && (
          <Alert variant="destructive">
            <AlertTitle>Deploy rejected</AlertTitle>
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
            id={`deploy-confirm-${service.id}`}
            checked={confirmed}
            disabled={streaming}
            onCheckedChange={(checked) => setConfirmed(checked === true)}
          />
          <Label htmlFor={`deploy-confirm-${service.id}`} className="font-normal">
            I confirm redeploying {service.name} (brief downtime expected).
          </Label>
        </div>

        <div>
          <Button
            type="button"
            onClick={() => void handleStart()}
            disabled={!confirmed || streaming}
            aria-label="Start deploy"
          >
            {streaming ? 'Deploy running…' : 'Start deploy'}
          </Button>
        </div>

        {resolvedTarget !== null && (
          <p className="text-muted-foreground text-sm">
            Accepted: redeploying {service.name} ({resolvedTarget}).
          </p>
        )}

        {lines.length > 0 && (
          <ul
            aria-label="deploy progress"
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
