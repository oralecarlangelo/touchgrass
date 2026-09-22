import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from './ErrorState.tsx';
import {
  createKey,
  fetchKeys,
  isUnauthorized,
  revokeKey,
  type ApiKey,
  type CreatedApiKey,
  type ServiceView,
} from '@/lib/api.ts';
import { formatTime } from '@/lib/format.ts';

const sampleRates = ['1', '0.5', '0.1', '0.01'];

export default function KeysScreen({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState(services[0]?.id ?? '');
  const [keys, setKeys] = useState<ApiKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sampleRate, setSampleRate] = useState('1');
  const [created, setCreated] = useState<CreatedApiKey | null>(null);
  const [pendingRevoke, setPendingRevoke] = useState<ApiKey | null>(null);
  const [working, setWorking] = useState(false);

  const effectiveId = services.some((service) => service.id === serviceId)
    ? serviceId
    : (services[0]?.id ?? '');

  const load = useCallback(async () => {
    if (effectiveId === '') {
      setKeys([]);
      return;
    }

    setError(null);

    try {
      setKeys(await fetchKeys(effectiveId));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [effectiveId, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  async function handleCreate(): Promise<void> {
    setWorking(true);

    try {
      const key = await createKey(effectiveId, Number(sampleRate));
      setCreated(key);
      toast.success('API key created — copy it now, it shows once.');
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setWorking(false);
    }
  }

  async function handleRevoke(): Promise<void> {
    if (pendingRevoke === null) {
      return;
    }

    setWorking(true);

    try {
      await revokeKey(pendingRevoke.id);
      toast.success(`Key ${pendingRevoke.key_prefix}… revoked.`);
      setPendingRevoke(null);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setWorking(false);
    }
  }

  function copyKey(value: string): void {
    void navigator.clipboard
      .writeText(value)
      .then(() => toast.success('Copied to clipboard.'))
      .catch(() => toast.error('Copy failed — select the key manually.'));
  }

  if (error !== null) {
    return <ErrorState title="Couldn't load API keys" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">API keys</h1>
        <Select value={effectiveId} onValueChange={setServiceId}>
          <SelectTrigger className="ml-auto w-48" aria-label="Service filter">
            <SelectValue placeholder="Pick a service" />
          </SelectTrigger>
          <SelectContent>
            {services.map((service) => (
              <SelectItem key={service.id} value={service.id}>
                {service.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">Ingest keys for error tracking</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {effectiveId === '' ? (
            <p className="text-muted-foreground text-sm">No services on the radar yet.</p>
          ) : (
            <>
              <div className="flex flex-wrap items-end gap-2">
                <div className="space-y-1">
                  <label htmlFor="sample-rate" className="text-sm font-medium">
                    Sample rate
                  </label>
                  <Select value={sampleRate} onValueChange={setSampleRate}>
                    <SelectTrigger id="sample-rate" className="w-32">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {sampleRates.map((rate) => (
                        <SelectItem key={rate} value={rate}>
                          {rate}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <Button onClick={() => void handleCreate()} disabled={working}>
                  {working ? 'Creating…' : 'Create key'}
                </Button>
              </div>

              {keys === null ? (
                <div className="space-y-2" aria-label="Loading keys">
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : keys.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  No keys yet — create one and set it as the SDK key.
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Prefix</TableHead>
                      <TableHead className="text-right">Sample rate</TableHead>
                      <TableHead>Created</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead className="text-right">Action</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {keys.map((key) => (
                      <TableRow key={key.id}>
                        <TableCell className="font-mono text-xs">{key.key_prefix}…</TableCell>
                        <TableCell className="text-right font-mono">{key.sample_rate}</TableCell>
                        <TableCell className="text-muted-foreground text-xs">
                          {formatTime(key.created_at)}
                        </TableCell>
                        <TableCell>
                          {key.revoked_at === null ? (
                            <Badge variant="default">active</Badge>
                          ) : (
                            <Badge variant="secondary">revoked</Badge>
                          )}
                        </TableCell>
                        <TableCell className="text-right">
                          {key.revoked_at === null && (
                            <Button
                              variant="ghost"
                              size="sm"
                              className="text-destructive hover:text-destructive"
                              onClick={() => setPendingRevoke(key)}
                              aria-label={`Revoke key ${key.key_prefix}`}
                            >
                              Revoke
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </>
          )}
        </CardContent>
      </Card>

      <Dialog open={created !== null} onOpenChange={(open) => !open && setCreated(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Key created</DialogTitle>
            <DialogDescription>
              Copy it now — the full key is never shown again.
            </DialogDescription>
          </DialogHeader>
          {created !== null && (
            <div className="flex items-center gap-2">
              <code className="bg-muted min-w-0 flex-1 truncate rounded-md px-3 py-2 font-mono text-xs">
                {created.key}
              </code>
              <Button variant="outline" size="sm" onClick={() => copyKey(created.key)}>
                Copy
              </Button>
            </div>
          )}
          <DialogFooter>
            <Button onClick={() => setCreated(null)}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={pendingRevoke !== null} onOpenChange={(open) => !open && setPendingRevoke(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Revoke this key?</DialogTitle>
            <DialogDescription>
              SDKs using {pendingRevoke?.key_prefix}… will stop ingesting immediately. This
              can&apos;t be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingRevoke(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void handleRevoke()} disabled={working}>
              {working ? 'Revoking…' : 'Revoke key'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
