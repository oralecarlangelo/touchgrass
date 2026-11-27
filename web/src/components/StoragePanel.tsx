import { useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { fetchStorageUsage, fetchSystem, isUnauthorized, type StorageUsage, type SystemSnapshot } from '@/lib/api';
import { formatBytes } from '@/lib/format';
import { snapshotHeartbeat } from '@/lib/heartbeat.ts';
import HeartbeatStatus from './HeartbeatStatus.tsx';

export default function StoragePanel({ revision, onUnauthorized }: { revision: number; onUnauthorized: () => void }) {
  const [usage, setUsage] = useState<StorageUsage | null>(null);
  const [host, setHost] = useState<SystemSnapshot | null>(null);
  const [usageError, setUsageError] = useState('');
  const [hostError, setHostError] = useState('');
  const [usageLoading, setUsageLoading] = useState(true);
  const [hostLoading, setHostLoading] = useState(true);
  const [refreshAttempt, setRefreshAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setUsageLoading(true);
    void fetchStorageUsage(controller.signal).then((next) => {
      if (!controller.signal.aborted) { setUsage(next); setUsageError(''); }
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      if (isUnauthorized(error)) { onUnauthorized(); return; }
      setUsageError('Docker accounting unavailable.');
    }).finally(() => { if (!controller.signal.aborted) setUsageLoading(false); });
    return () => controller.abort();
  }, [onUnauthorized, refreshAttempt, revision]);
  useEffect(() => {
    const controller = new AbortController();
    setHostLoading(true);
    void fetchSystem(controller.signal).then((next) => {
      if (!controller.signal.aborted) { setHost(next); setHostError(''); }
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      if (isUnauthorized(error)) { onUnauthorized(); return; }
      setHostError('Host capacity unavailable.');
    }).finally(() => { if (!controller.signal.aborted) setHostLoading(false); });
    return () => controller.abort();
  }, [onUnauthorized, refreshAttempt, revision]);
  const loading = usageLoading || hostLoading;
  const hostPending = host === null && hostLoading;
  const usagePending = usage === null && usageLoading;
  const used = host?.disk_used_bytes;
  const total = host?.disk_total_bytes;
  const percent = used != null && total != null && total > 0 ? used / total * 100 : null;
  return <Card>
    <CardHeader className="flex flex-row items-center justify-between">
      <CardTitle>Disk usage</CardTitle>
      <Button variant="outline" disabled={loading} onClick={() => setRefreshAttempt((attempt) => attempt + 1)}>{loading ? 'Loading…' : 'Refresh usage'}</Button>
    </CardHeader>
    <CardContent className="space-y-4">
      {hostError && <p role="alert" className="text-destructive text-sm">{hostError}{host === null ? '' : ' Showing last known host capacity.'}</p>}
      {usageError && <p role="alert" className="text-destructive text-sm">{usageError}{usage === null ? '' : ' Showing last known Docker accounting.'}</p>}
      {usage && (
        <HeartbeatStatus
          label="Docker disk accounting heartbeat"
          state={snapshotHeartbeat(usage.freshness)}
          observedAt={usage.freshness.observed_at}
          detail={usage.freshness.last_error_category === 'collection_failed'
            ? 'Docker accounting failed. Check daemon access and verify that Docker can report disk usage.'
            : usage.freshness.stale && usage.freshness.observed_at === null ? 'Waiting for the first disk accounting sample.'
              : usage.freshness.stale ? 'Showing the last accounting sample. Check the collector if the next sample is also late.' : undefined}
          action={usage.freshness.last_error_category === 'collection_failed'
            ? <a href="#/fleet" className="inline-block underline underline-offset-4">Open Fleet</a> : null}
        />
      )}
      <div className="grid gap-4 sm:grid-cols-3">
        <div><p className="text-muted-foreground text-sm">Host filesystem</p><p className="text-xl font-semibold">{hostPending ? 'Loading capacity…' : used == null || total == null ? 'Unavailable' : `${formatBytes(used)} / ${formatBytes(total)}`}</p></div>
        <div><p className="text-muted-foreground text-sm">Available</p><p className="text-xl font-semibold">{hostPending ? 'Loading capacity…' : used == null || total == null ? 'Unavailable' : formatBytes(Math.max(0, total - used))}</p></div>
        <div><p className="text-muted-foreground text-sm">Docker image layers (shared layers counted once)</p><p className="text-xl font-semibold">{usagePending ? 'Loading snapshot…' : usage?.image_layers_bytes == null ? 'Unavailable' : formatBytes(usage.image_layers_bytes)}</p></div>
      </div>
      {percent !== null && <><progress className="h-3 w-full" max={100} value={Math.min(100, percent)} aria-label="Host disk used" /><p className={percent >= 90 ? 'text-destructive text-sm font-medium' : 'text-muted-foreground text-sm'}>{percent.toFixed(1)}% used{percent >= 90 ? ' — low disk space. Review the largest consumers before the next deployment.' : ''}</p></>}
      <p className="text-muted-foreground text-sm">Container sizes are writable layers only. Bind mounts, container logs, host logs and backups are not included below. Build cache can share image layers; these figures must not be added into a host total. Unreferenced volumes may still hold valuable data.</p>
      {usage && usage.freshness.observed_at && <><p className="text-muted-foreground text-xs">{usage.entries.length} objects · largest first</p>
        <div className="max-h-96 overflow-auto"><table className="w-full text-left text-sm"><thead><tr><th className="p-2">Object</th><th className="p-2">Type</th><th className="p-2">Size</th><th className="p-2">Status</th></tr></thead><tbody>
          {usage.entries.map(entry => <tr className="border-t" key={`${entry.kind}:${entry.id}`}><td className="max-w-96 break-all p-2 font-mono text-xs">{entry.name}</td><td className="p-2">{entry.kind}</td><td className="whitespace-nowrap p-2">{entry.size_bytes === null ? 'Unknown' : formatBytes(entry.size_bytes)}</td><td className="p-2">{entry.status}</td></tr>)}
        </tbody></table>{usage.entries.length === 0 && <p className="py-4 text-sm">No volumes, writable layers or build cache reported.</p>}</div></>}
    </CardContent>
  </Card>;
}
