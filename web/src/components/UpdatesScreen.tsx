import { useCallback, useEffect, useRef, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { Skeleton } from '@/components/ui/skeleton';
import { apiErrorCode, fetchSelfUpdate, isUnauthorized, startSelfUpdate, type SelfUpdateStatus } from '@/lib/api.ts';

const updaterGuide = 'https://github.com/oralecarlangelo/touchgrass/blob/develop/docs/native-updates.md';

export default function UpdatesScreen({ onUnauthorized }: { onUnauthorized: () => void }) {
  const initialVersion = useRef<string | null>(null);
  const reloadRequested = useRef(false);
  const reading = useRef(false);
  const jobState = useRef<SelfUpdateStatus['job']>(null);
  const [status, setStatus] = useState<SelfUpdateStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [installError, setInstallError] = useState<{ message: string; code: string } | null>(null);
  const [working, setWorking] = useState(false);
  const [checking, setChecking] = useState(false);
  const [checkedAt, setCheckedAt] = useState<string | null>(null);
  const [reconnecting, setReconnecting] = useState(false);

  const refresh = useCallback(async (force = false) => {
    if (reading.current) return;
    reading.current = true;
    setChecking(true);
    try {
      const next = await fetchSelfUpdate(force);
      if (!reloadRequested.current && shouldReloadUpdatedConsole(initialVersion.current, next, pendingUpdate())) {
        reloadRequested.current = true;
        pendingUpdate(null);
        window.location.reload();
        return;
      }
      initialVersion.current ??= next.installed;
      jobState.current = next.job;
      if (next.job?.state === 'failed') pendingUpdate(null);
      setCheckedAt(new Date().toISOString());
      setStatus(next);
      setError(null);
      setReconnecting(false);
    } catch (err) {
      if (isUnauthorized(err)) {
        if (pendingUpdate() && !reloadRequested.current) {
          reloadRequested.current = true;
          pendingUpdate(null);
          window.location.reload();
          return;
        }
        onUnauthorized();
        return;
      }
      if (jobState.current?.state === 'pending' || jobState.current?.state === 'running' || pendingUpdate() !== null) {
        setReconnecting(true);
      } else {
        setError(err instanceof Error ? err.message : String(err));
      }
    } finally {
      reading.current = false;
      setChecking(false);
    }
  }, [onUnauthorized]);

  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => {
    if (status?.job?.state !== 'pending' && status?.job?.state !== 'running' && !reconnecting) return;
    const refreshVisible = () => { if (!document.hidden) void refresh(); };
    const timer = window.setInterval(refreshVisible, 5000);
    document.addEventListener('visibilitychange', refreshVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', refreshVisible);
    };
  }, [refresh, status?.job?.state, reconnecting]);

  async function install(): Promise<void> {
    const tag = status?.release?.tag;
    if (!tag) return;
    pendingUpdate(tag);
    setWorking(true);
    setInstallError(null);
    setReconnecting(true);
    try {
      const next = await startSelfUpdate(tag);
      jobState.current = next.job;
      setStatus(next);
      setConfirming(false);
    } catch (err) {
      if (isUnauthorized(err)) { pendingUpdate(null); onUnauthorized(); }
      else if (err instanceof Error && err.message.startsWith('API unreachable:')) {
        setConfirming(false);
        // The detached job may have restarted the console before its 202 reached us.
      } else {
        pendingUpdate(null);
        setReconnecting(false);
        setInstallError({ message: err instanceof Error ? err.message : String(err), code: apiErrorCode(err) });
      }
    } finally {
      setWorking(false);
    }
  }

  return <div className="space-y-5">
    <div className="flex items-center justify-between gap-3">
      <div><h1 className="text-xl font-semibold">Updates</h1><p className="text-sm text-muted-foreground">Install a published Touch Grass release.</p></div>
      <Button variant="outline" disabled={checking || working || reconnecting} onClick={() => void refresh(true)}>{checking ? 'Checking…' : 'Check again'}</Button>
    </div>
    {status === null && error === null ? <Skeleton className="h-48 w-full" /> : null}
    {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
    {status && <Card>
      <CardHeader><CardTitle>Console version</CardTitle></CardHeader>
      <CardContent className="space-y-4 text-sm">
        <div>Installed: <strong>{status.installed}</strong></div>
        {checkedAt !== null && <p className="text-muted-foreground text-xs">Console status checked at {new Date(checkedAt).toLocaleTimeString()}.</p>}
        {!status.configured && <p>This older installation needs a one-time updater setup. Once enabled, future updates install here without extra configuration.</p>}
        {!status.configured && <a className="underline" href={updaterGuide} target="_blank" rel="noreferrer">Set up updates</a>}
        {status.check_error && <p role="status">{status.check_error}</p>}
        {status.release && <div className="space-y-1">
          <div>Latest published release: <strong>{status.release.tag}</strong></div>
          <a className="underline" href={status.release.url} target="_blank" rel="noreferrer">Review release notes</a>
        </div>}
        {status.release && !status.can_install && <p role="status">{updateVersionMessage(status)}</p>}
        {status.job && <div aria-live="polite">Last update: <Badge variant="secondary">{status.job.state}</Badge> {status.job.tag}</div>}
        {reconnecting && <p role="status">Console restarting. Reconnecting… You may need to sign in again.</p>}
        {status.job?.state === 'failed' && <p role="alert">The update failed. Use the deployment recovery workflow if the console needs repair.</p>}
        <Button disabled={!status.can_install || working || reconnecting} onClick={() => {
          setInstallError(null);
          setConfirming(true);
        }}>Install update</Button>
      </CardContent>
    </Card>}
    <Dialog open={confirming} onOpenChange={setConfirming}>
      <DialogContent><DialogHeader><DialogTitle>Install {status?.release?.tag}?</DialogTitle>
        <DialogDescription>The console will briefly restart. You may need to sign in again. Review the release notes first; use the deployment workflow if this release needs host configuration or migration work.</DialogDescription>
      </DialogHeader>
        {installError !== null && (
          <div className="space-y-1 text-sm" role="alert">
            <p className="text-destructive">{installError.message}</p>
            {installError.code === 'audit_unavailable' && (
              <p>Ask the host administrator to check the audit store and follow the{' '}
                <a className="underline" href={`${updaterGuide}#update-request-could-not-be-recorded`} target="_blank" rel="noreferrer">host recovery guide</a>.
              </p>
            )}
          </div>
        )}
        <DialogFooter><Button variant="outline" onClick={() => setConfirming(false)}>Cancel</Button>
        <Button disabled={working} onClick={() => void install()}>{working ? 'Starting…' : 'Install update'}</Button>
      </DialogFooter></DialogContent>
    </Dialog>
  </div>;
}

export function updateVersionMessage(status: SelfUpdateStatus): string {
  if (status.job?.state === 'pending' || status.job?.state === 'running') return 'Update in progress.';
  if (!status.release) return '';
  if (`v${status.installed.replace(/^v/, '')}` === status.release.tag) return 'You are running the latest published release.';
  if (/[-+]/.test(status.installed)) return 'This is a custom build. Updates are offered when a newer base version is published.';
  if (!status.configured) return 'An available release can be installed after the one-time setup.';
  return 'No newer compatible release is available.';
}

export function shouldReloadUpdatedConsole(previous: string | null, next: SelfUpdateStatus, requested: string | null): boolean {
  if (next.job?.state !== 'succeeded' || next.job.tag !== `v${next.installed.replace(/^v/, '')}`) return false;
  return (previous !== null && previous !== next.installed) || requested === next.job.tag;
}

function pendingUpdate(value?: string | null): string | null {
  try {
    if (value === null) sessionStorage.removeItem('touchgrass-pending-update');
    else if (value !== undefined) sessionStorage.setItem('touchgrass-pending-update', value);
    return sessionStorage.getItem('touchgrass-pending-update');
  } catch {
    // Restricted browser storage still permits in-page version detection.
    return null;
  }
}
