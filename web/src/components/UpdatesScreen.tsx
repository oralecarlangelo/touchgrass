import { useCallback, useEffect, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { fetchSelfUpdate, isUnauthorized, startSelfUpdate, type SelfUpdateStatus } from '@/lib/api.ts';

export default function UpdatesScreen({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [status, setStatus] = useState<SelfUpdateStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [working, setWorking] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const next = await fetchSelfUpdate();
      setStatus(next);
      setError(null);
      setReconnecting(false);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }
      if (status?.job?.state === 'pending' || status?.job?.state === 'running') {
        setReconnecting(true);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  }, [onUnauthorized, status?.job?.state]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => {
    if (status?.job?.state !== 'pending' && status?.job?.state !== 'running' && !reconnecting) return;
    const timer = window.setInterval(() => void refresh(), 5000);
    return () => window.clearInterval(timer);
  }, [refresh, status?.job?.state, reconnecting]);

  async function install(): Promise<void> {
    const tag = status?.release?.tag;
    if (!tag) return;
    setWorking(true);
    setError(null);
    setReconnecting(true);
    try {
      setStatus(await startSelfUpdate(tag));
      setConfirming(false);
    } catch (err) {
      if (isUnauthorized(err)) onUnauthorized();
      else if (err instanceof Error && err.message.startsWith('API unreachable:')) {
        setConfirming(false);
        // The detached job may have restarted the console before its 202 reached us.
      } else {
        setReconnecting(false);
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      setWorking(false);
    }
  }

  return <div className="space-y-5">
    <div className="flex items-center justify-between gap-3">
      <div><h1 className="text-xl font-semibold">Updates</h1><p className="text-sm text-muted-foreground">Install a published Touch Grass release.</p></div>
      <Button variant="outline" onClick={() => void refresh()}>Check again</Button>
    </div>
    {status === null && error === null ? <Skeleton className="h-48 w-full" /> : null}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {status && <Card>
      <CardHeader><CardTitle>Console version</CardTitle></CardHeader>
      <CardContent className="space-y-4 text-sm">
        <div>Installed: <strong>{status.installed}</strong></div>
        {!status.configured && <p>The host update helper is not configured. Use the existing deployment workflow to install it.</p>}
        {status.check_error && <p role="status">{status.check_error}</p>}
        {status.release && <div className="space-y-1">
          <div>Latest compatible: <strong>{status.release.tag}</strong></div>
          <a className="underline" href={status.release.url} target="_blank" rel="noreferrer">Review release notes</a>
        </div>}
        {status.job && <div aria-live="polite">Last update: <Badge variant="secondary">{status.job.state}</Badge> {status.job.tag}</div>}
        {reconnecting && <p role="status">Console restarting. Reconnecting… You may need to sign in again.</p>}
        {status.job?.state === 'failed' && <p role="alert">The update failed. Use the deployment recovery workflow if the console needs repair.</p>}
        <Button disabled={!status.can_install || working || reconnecting} onClick={() => setConfirming(true)}>Install update</Button>
      </CardContent>
    </Card>}
    <Dialog open={confirming} onOpenChange={setConfirming}>
      <DialogContent><DialogHeader><DialogTitle>Install {status?.release?.tag}?</DialogTitle>
        <DialogDescription>The console will briefly restart. You may need to sign in again. Review the release notes first; use the deployment workflow if this release needs host configuration or migration work.</DialogDescription>
      </DialogHeader><DialogFooter><Button variant="outline" onClick={() => setConfirming(false)}>Cancel</Button>
        <Button disabled={working} onClick={() => void install()}>{working ? 'Starting…' : 'Install update'}</Button>
      </DialogFooter></DialogContent>
    </Dialog>
  </div>;
}
