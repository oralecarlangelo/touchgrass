import { useMemo, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { SecurityFinding } from '@/lib/api.ts';
import { recommendedResponse, severityLabel } from '@/lib/security.ts';
import { formatTime } from '@/lib/format.ts';

export default function FindingDetail({
  finding,
  isAdmin,
  busy,
  onClose,
  onContainer,
  onAcknowledge,
  onResolve,
  onSuppress,
}: {
  finding: SecurityFinding;
  isAdmin: boolean;
  busy: boolean;
  onClose: () => void;
  onContainer: (id: string) => void;
  onAcknowledge: () => void;
  onResolve: () => void;
  onSuppress: (reason: string, until: string | null) => void;
}) {
  const [reason, setReason] = useState('');
  const [expiryMode, setExpiryMode] = useState<'expires' | 'non-expiring'>('expires');
  const [until, setUntil] = useState('');
  const evidence = useMemo(() => Object.entries(finding.evidence).sort(([a], [b]) => a.localeCompare(b)), [finding.evidence]);
  const canSuppress = reason.trim() !== '' && (expiryMode === 'non-expiring' || until !== '');

  return (
    <section aria-label="Security finding detail" tabIndex={-1} className="space-y-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-muted-foreground text-xs uppercase tracking-wide">Finding #{finding.id} · episode {finding.episode}</p>
          <h2 className="text-lg font-semibold tracking-tight">{finding.rule_id}</h2>
        </div>
        <Button variant="outline" onClick={onClose}>Back to findings</Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="flex flex-wrap items-center gap-2 text-base">
              <Badge variant={finding.severity === 'critical' ? 'destructive' : 'secondary'}>
                {severityLabel(finding.severity)} severity
              </Badge>
              <Badge variant="outline">{finding.status}</Badge>
            </CardTitle>
            <CardDescription>{recommendedResponse(finding)}</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3 text-sm">
            <button className="text-primary font-mono text-xs underline underline-offset-4" type="button" onClick={() => onContainer(finding.container_id)}>
              Container {finding.container_id}
            </button>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">
              <dt className="text-muted-foreground">Category</dt><dd>{finding.category}</dd>
              <dt className="text-muted-foreground">Confidence</dt><dd>{finding.confidence}</dd>
              <dt className="text-muted-foreground">First seen</dt><dd>{formatTime(finding.opened_at)}</dd>
              <dt className="text-muted-foreground">Last seen</dt><dd>{formatTime(finding.last_seen_at)}</dd>
              <dt className="text-muted-foreground">Occurrences</dt><dd>{finding.occurrence_count}</dd>
              <dt className="text-muted-foreground">Fingerprint</dt><dd className="break-all font-mono text-xs">{finding.fingerprint}</dd>
            </dl>
          </CardContent>
        </Card>

        <Card>
          <CardHeader><CardTitle className="text-base">Bounded evidence</CardTitle></CardHeader>
          <CardContent>
            {evidence.length === 0 ? (
              <p className="text-muted-foreground text-sm">No evidence metadata was retained.</p>
            ) : (
              <dl className="space-y-2">
                {evidence.map(([key, value]) => (
                  <div key={key} className="rounded-md border p-2">
                    <dt className="text-muted-foreground text-xs">{key.replace(/_/g, ' ')}</dt>
                    <dd className="break-all font-mono text-xs">{value || 'n/a'}</dd>
                  </div>
                ))}
              </dl>
            )}
          </CardContent>
        </Card>
      </div>

      {isAdmin ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Operator workflow</CardTitle>
            <CardDescription>These actions record your authenticated username in the audit trail. They do not contain or stop a container.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" disabled={busy || finding.status !== 'open'} onClick={onAcknowledge}>Acknowledge</Button>
              <Button disabled={busy || finding.status === 'resolved'} onClick={onResolve}>Resolve</Button>
            </div>
            <div className="grid gap-3 md:grid-cols-3">
              <div className="space-y-1.5 md:col-span-3">
                <Label htmlFor="suppression-reason">Suppression reason</Label>
                <Input id="suppression-reason" value={reason} onChange={(event) => setReason(event.target.value)} placeholder="Why is this safe to suppress?" />
              </div>
              <div className="space-y-1.5">
                <Label>Duration</Label>
                <Select value={expiryMode} onValueChange={(value) => setExpiryMode(value as typeof expiryMode)}>
                  <SelectTrigger aria-label="Suppression duration"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="expires">Expires</SelectItem>
                    <SelectItem value="non-expiring">Non-expiring</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {expiryMode === 'expires' && (
                <div className="space-y-1.5">
                  <Label htmlFor="suppression-until">Expires at</Label>
                  <Input id="suppression-until" type="datetime-local" value={until} onChange={(event) => setUntil(event.target.value)} />
                </div>
              )}
              <div className="flex items-end">
                <Button variant="secondary" disabled={busy || !canSuppress || finding.status === 'resolved'} onClick={() => onSuppress(reason.trim(), expiryMode === 'non-expiring' ? null : new Date(until).toISOString())}>Suppress</Button>
              </div>
            </div>
          </CardContent>
        </Card>
      ) : (
        <p className="text-muted-foreground text-sm">Read-only access. An admin can acknowledge, resolve, or suppress this finding.</p>
      )}
    </section>
  );
}
