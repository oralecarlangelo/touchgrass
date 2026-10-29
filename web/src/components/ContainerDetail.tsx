import { useCallback, useEffect, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import ErrorState from './ErrorState.tsx';
import {
  fetchContainer,
  fetchSecurityFindings,
  isUnauthorized,
  type ContainerDetailResponse,
  type SecurityFinding,
} from '@/lib/api.ts';

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b py-2 last:border-0">
      <dt className="text-muted-foreground text-sm">{label}</dt>
      <dd className="max-w-[70%] break-all text-right font-mono text-xs">{value}</dd>
    </div>
  );
}

export function ContainerDetailView({ detail, findings = [], onSelectFinding }: { detail: ContainerDetailResponse; findings?: SecurityFinding[]; onSelectFinding?: (id: number) => void }) {
  const { container, inspection } = detail;

  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            {container.name}
            <Badge variant={container.state === 'running' ? 'default' : 'secondary'}>
              {container.state}
            </Badge>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {!inspection.coverage_complete && (
            <p role="status" className="mb-4 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
              Docker inspection is currently unavailable. Values below are the last known posture and must not be treated as current.
            </p>
          )}
          <dl>
            <DetailRow label="Instance ID" value={container.id} />
            <DetailRow label="Image" value={detail.image || 'n/a'} />
            <DetailRow label="Image ID" value={detail.image_id || 'n/a'} />
            <DetailRow label="Classification" value={container.classification} />
            <DetailRow label="Health" value={container.health} />
            <DetailRow label="Last seen" value={container.last_seen_at ?? 'n/a'} />
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Security posture</CardTitle>
        </CardHeader>
        <CardContent>
          <dl>
            <DetailRow label="Runtime user" value={inspection.user || 'root/default'} />
            <DetailRow label="Privileged" value={inspection.privileged ? 'yes' : 'no'} />
            <DetailRow label="Read-only root" value={inspection.readonly_root_fs ? 'yes' : 'no'} />
            <DetailRow label="PID mode" value={inspection.pid_mode ?? 'default'} />
            <DetailRow label="Network mode" value={inspection.network_mode ?? 'default'} />
            <DetailRow label="Added capabilities" value={inspection.cap_add.join(', ') || 'none'} />
            <DetailRow label="Docker socket" value={inspection.has_docker_socket ? 'mounted' : 'not mounted'} />
            <DetailRow label="Command fingerprint" value={inspection.command_hash ?? 'n/a'} />
          </dl>
          <div className="mt-4 space-y-2">
            <p className="text-muted-foreground text-xs font-medium uppercase tracking-wide">Mounts</p>
            {inspection.mounts.length === 0 ? (
              <p className="text-muted-foreground text-sm">No mounts reported.</p>
            ) : (
              inspection.mounts.map((mount) => (
                <div key={`${mount.type}:${mount.destination}`} className="rounded-md border p-2 text-xs">
                  <span className="font-mono">{mount.destination}</span>
                  <span className="text-muted-foreground"> · {mount.type} · {mount.writable ? 'rw' : 'ro'}</span>
                </div>
              ))
            )}
          </div>
        </CardContent>
      </Card>

      <Card className="lg:col-span-2">
        <CardHeader><CardTitle className="text-base">Security findings</CardTitle></CardHeader>
        <CardContent>
          {findings.length === 0 ? <p className="text-muted-foreground text-sm">No finding episodes are linked to this container.</p> : (
            <ul className="divide-y">
              {findings.map((finding) => <li key={finding.id} className="flex flex-wrap items-center gap-2 py-2"><Badge variant={finding.severity === 'critical' ? 'destructive' : 'secondary'}>{finding.severity}</Badge><button type="button" className="text-primary font-mono text-xs underline underline-offset-4" onClick={() => onSelectFinding?.(finding.id)}>{finding.rule_id}</button><span className="text-muted-foreground ml-auto text-xs">{finding.status}</span></li>)}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default function ContainerDetail({
  id,
  onBack,
  onUnauthorized,
  onSelectSecurityFinding,
}: {
  id: string;
  onBack: () => void;
  onUnauthorized: () => void;
  onSelectSecurityFinding?: (id: number) => void;
}) {
  const [detail, setDetail] = useState<ContainerDetailResponse | null>(null);
  const [findings, setFindings] = useState<SecurityFinding[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [nextDetail, nextFindings] = await Promise.all([
        fetchContainer(id), fetchSecurityFindings({ containerId: id, limit: 100, sort: 'severity' }),
      ]);
      setDetail(nextDetail);
      setFindings(nextFindings);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [id, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <p className="text-muted-foreground text-xs uppercase tracking-wide">Container instance</p>
          <h1 className="text-lg font-semibold tracking-tight">{detail?.container.name ?? id}</h1>
        </div>
        <Button variant="outline" onClick={onBack}>Back to fleet</Button>
      </div>
      {error !== null ? (
        <ErrorState title="Couldn't load container" message={error} onRetry={() => void load()} />
      ) : detail === null ? (
        <div className="grid gap-4 lg:grid-cols-2" aria-label="Loading container detail">
          <Skeleton className="h-72 w-full" />
          <Skeleton className="h-72 w-full" />
        </div>
      ) : (
        <ContainerDetailView detail={detail} findings={findings} onSelectFinding={onSelectSecurityFinding} />
      )}
    </div>
  );
}
