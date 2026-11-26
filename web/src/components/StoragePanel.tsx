import { useCallback, useEffect, useState } from 'react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { fetchStorageUsage, fetchSystem, isUnauthorized, type StorageUsage, type SystemSnapshot } from '@/lib/api';
import { formatBytes, formatTime } from '@/lib/format';

export default function StoragePanel({ revision, onUnauthorized }: { revision: number; onUnauthorized: () => void }) {
  const [usage, setUsage] = useState<StorageUsage | null>(null);
  const [host, setHost] = useState<SystemSnapshot | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    const results = await Promise.allSettled([fetchStorageUsage(), fetchSystem()]);
    const failures: string[] = [];
    if (results[0].status === 'fulfilled') setUsage(results[0].value);
    else { setUsage(null); failures.push('Docker accounting unavailable.'); }
    if (results[1].status === 'fulfilled') setHost(results[1].value);
    else { setHost(null); failures.push('Host capacity unavailable.'); }
    for (const result of results) if (result.status === 'rejected' && isUnauthorized(result.reason)) onUnauthorized();
    setError(failures.join(' '));
    setLoading(false);
  }, [onUnauthorized]);
  useEffect(() => { void load(); }, [load, revision]);
  const used = host?.disk_used_bytes;
  const total = host?.disk_total_bytes;
  const percent = used != null && total != null && total > 0 ? used / total * 100 : null;
  return <Card>
    <CardHeader className="flex flex-row items-center justify-between">
      <CardTitle>Disk usage</CardTitle>
      <Button variant="outline" disabled={loading} onClick={() => void load()}>{loading ? 'Inspecting…' : 'Refresh usage'}</Button>
    </CardHeader>
    <CardContent className="space-y-4">
      {error && <p role="alert" className="text-destructive text-sm">{error}</p>}
      <div className="grid gap-4 sm:grid-cols-3">
        <div><p className="text-muted-foreground text-sm">Host filesystem</p><p className="text-xl font-semibold">{used == null || total == null ? 'Unavailable' : `${formatBytes(used)} / ${formatBytes(total)}`}</p></div>
        <div><p className="text-muted-foreground text-sm">Available</p><p className="text-xl font-semibold">{used == null || total == null ? 'Unavailable' : formatBytes(Math.max(0, total - used))}</p></div>
        <div><p className="text-muted-foreground text-sm">Docker image layers (shared layers counted once)</p><p className="text-xl font-semibold">{usage?.image_layers_bytes == null ? 'Unavailable' : formatBytes(usage.image_layers_bytes)}</p></div>
      </div>
      {percent !== null && <><progress className="h-3 w-full" max={100} value={Math.min(100, percent)} aria-label="Host disk used" /><p className={percent >= 90 ? 'text-destructive text-sm font-medium' : 'text-muted-foreground text-sm'}>{percent.toFixed(1)}% used{percent >= 90 ? ' — low disk space. Review the largest consumers before the next deployment.' : ''}</p></>}
      <p className="text-muted-foreground text-sm">Container sizes are writable layers only. Bind mounts, container logs, host logs and backups are not included below. Build cache can share image layers; these figures must not be added into a host total. Unreferenced volumes may still hold valuable data.</p>
      {usage && <><p className="text-muted-foreground text-xs">Inspected {formatTime(usage.sampled_at)} · {usage.entries.length} objects · largest first</p>
        <div className="max-h-96 overflow-auto"><table className="w-full text-left text-sm"><thead><tr><th className="p-2">Object</th><th className="p-2">Type</th><th className="p-2">Size</th><th className="p-2">Status</th></tr></thead><tbody>
          {usage.entries.map(entry => <tr className="border-t" key={`${entry.kind}:${entry.id}`}><td className="max-w-96 break-all p-2 font-mono text-xs">{entry.name}</td><td className="p-2">{entry.kind}</td><td className="whitespace-nowrap p-2">{entry.size_bytes === null ? 'Unknown' : formatBytes(entry.size_bytes)}</td><td className="p-2">{entry.status}</td></tr>)}
        </tbody></table>{usage.entries.length === 0 && <p className="py-4 text-sm">No volumes, writable layers or build cache reported.</p>}</div></>}
    </CardContent>
  </Card>;
}
