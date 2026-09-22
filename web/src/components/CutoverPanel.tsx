import { useCallback, useState } from 'react';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { useOperationEvents } from '../hooks/useOperationEvents.ts';
import { startCutover, type ServiceView } from '../lib/api.ts';

export default function CutoverPanel({
  service,
  onFinished,
}: {
  service: ServiceView;
  onFinished: () => void;
}) {
  const [target, setTarget] = useState('auto');
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
    operationNoun: 'cutover',
    onFinished: handleStreamFinished,
    onStreamError: handleStreamError,
  });

  async function handleStart(): Promise<void> {
    setStartError(null);
    clear();
    setResolvedTarget(null);

    try {
      const response = await startCutover(service.id, target === 'auto' ? '' : target);
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
        <CardTitle>Cutover</CardTitle>
        <CardDescription>
          Live color: {service.live_color === '' ? 'unknown' : service.live_color}. The configured
          cutover script runs its preflight checks before traffic moves.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {startError !== null && (
          <Alert variant="destructive">
            <AlertTitle>Cutover rejected</AlertTitle>
            <AlertDescription>{startError}</AlertDescription>
          </Alert>
        )}

        {streamError !== null && (
          <Alert variant="destructive">
            <AlertTitle>Stream interrupted</AlertTitle>
            <AlertDescription>{streamError}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-wrap items-end gap-2">
          <div className="space-y-1">
            <Label htmlFor={`cutover-target-${service.id}`}>Target</Label>
            <Select value={target} onValueChange={setTarget} disabled={streaming}>
              <SelectTrigger id={`cutover-target-${service.id}`} className="w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auto">auto (idle color)</SelectItem>
                <SelectItem value="blue">blue</SelectItem>
                <SelectItem value="green">green</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <Button
            type="button"
            onClick={() => void handleStart()}
            disabled={!confirmed || streaming}
            aria-label="Start cutover"
          >
            {streaming ? 'Cutover running…' : 'Start cutover'}
          </Button>
        </div>

        <div className="flex items-start gap-2">
          <Checkbox
            id={`cutover-confirm-${service.id}`}
            checked={confirmed}
            disabled={streaming}
            onCheckedChange={(checked) => setConfirmed(checked === true)}
          />
          <Label htmlFor={`cutover-confirm-${service.id}`} className="font-normal">
            I confirm flipping live traffic for {service.name}
            {target === 'auto' ? ' to the idle color' : ` to ${target}`}.
          </Label>
        </div>

        {target !== 'auto' && target === service.live_color && (
          <p className="text-sm text-amber-600 dark:text-amber-400">
            Target is already live. Starting will re-run the cutover against the same color.
          </p>
        )}

        {resolvedTarget !== null && (
          <p className="text-muted-foreground text-sm">
            Accepted: flipping {service.name} to {resolvedTarget}.
          </p>
        )}

        {lines.length > 0 && (
          <ul
            aria-label="cutover progress"
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
