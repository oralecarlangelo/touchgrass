import { useCallback, useState } from 'react';
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
import { formatTime, timeAgo } from '@/lib/format.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

const sampleRates = ['1', '0.5', '0.1', '0.01'];

const sdkSetup = `import { init } from '@touchgrass/node';

init({
  endpoint: process.env.TOUCHGRASS_ENDPOINT ?? '',
  key: process.env.TOUCHGRASS_KEY ?? '',
});`;

export default function KeysScreen({
  services,
  onUnauthorized,
  isAdmin,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
  isAdmin: boolean;
}) {
  const [serviceId, setServiceId] = useState(services[0]?.id ?? '');
  const [sampleRate, setSampleRate] = useState('1');
  const [created, setCreated] = useState<CreatedApiKey | null>(null);
  const [pendingRevoke, setPendingRevoke] = useState<ApiKey | null>(null);
  const [working, setWorking] = useState(false);

  const effectiveId = services.some((service) => service.id === serviceId)
    ? serviceId
    : (services[0]?.id ?? '');

  const readKeys = useCallback((signal: AbortSignal) => fetchKeys(effectiveId, signal), [effectiveId]);
  const { data: keys, error, retry } = usePanelRead(effectiveId, effectiveId ? readKeys : null, 0, onUnauthorized);

  async function handleCreate(): Promise<void> {
    setWorking(true);

    try {
      const key = await createKey(effectiveId, Number(sampleRate));
      setCreated(key);
      toast.success('API key created — copy it now, it shows once.');
      retry();
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
      retry();
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
      .catch(() => toast.error('Could not copy. Select the text and copy it manually.'));
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
          <CardTitle className="text-sm font-medium">SDK ingest keys</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {effectiveId === '' ? (
            <p className="text-muted-foreground text-sm">Add a service to create an ingest key.</p>
          ) : (
            <>
              {isAdmin && (
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
              )}

              <details className="rounded-md border p-3">
                <summary className="cursor-pointer text-sm font-medium">Connect the Node SDK</summary>
                <p className="text-muted-foreground my-2 text-sm">
                  Use a key from this service. Set TOUCHGRASS_ENDPOINT to this console URL and store
                  TOUCHGRASS_KEY in the application environment.
                </p>
                <pre className="bg-muted overflow-auto rounded-md p-2 text-xs"><code>{sdkSetup}</code></pre>
                <div className="mt-2 flex items-center gap-3">
                  <Button variant="outline" size="sm" onClick={() => copyKey(sdkSetup)}>Copy setup snippet</Button>
                  <a className="text-primary text-sm underline underline-offset-4" href="/docs/sdk" target="_blank" rel="noreferrer">SDK guide</a>
                </div>
              </details>

              {error !== null && <ErrorState title="Couldn't refresh API keys" message={error} onRetry={retry} />}
              {keys === null ? error === null && (
                <div className="space-y-2" aria-label="Loading keys">
                  <Skeleton className="h-10 w-full" />
                </div>
              ) : keys.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  No ingest keys for this service yet.
                </p>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Prefix</TableHead>
                      <TableHead className="hidden text-right sm:table-cell">Sample rate</TableHead>
                      <TableHead className="hidden lg:table-cell">Created</TableHead>
                      <TableHead>Last used</TableHead>
                      <TableHead className="hidden sm:table-cell">Status</TableHead>
                      {isAdmin && <TableHead className="text-right">Action</TableHead>}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {keys.map((key) => {
                      const revoked = key.revoked_at !== null;
                      const status = <Badge variant={revoked ? 'secondary' : 'default'}>{revoked ? 'revoked' : 'active'}</Badge>;
                      return (
                        <TableRow key={key.id}>
                          <TableCell className="font-mono text-xs">
                            {key.key_prefix}…
                            <span className="text-muted-foreground block sm:hidden">Sample {key.sample_rate}</span>
                            <span className="mt-1 block font-sans sm:hidden">{status}</span>
                          </TableCell>
                          <TableCell className="hidden text-right font-mono sm:table-cell">{key.sample_rate}</TableCell>
                          <TableCell className="text-muted-foreground hidden text-xs lg:table-cell">
                            {formatTime(key.created_at)}
                          </TableCell>
                          <TableCell className="text-muted-foreground text-xs whitespace-normal">
                            {key.last_used_at ? <time dateTime={key.last_used_at} title={formatTime(key.last_used_at)}>{timeAgo(key.last_used_at)}</time> : 'No use recorded'}
                          </TableCell>
                          <TableCell className="hidden sm:table-cell">{status}</TableCell>
                          {isAdmin && (
                            <TableCell className="text-right">
                              {!revoked && (
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
                          )}
                        </TableRow>
                      );
                    })}
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
