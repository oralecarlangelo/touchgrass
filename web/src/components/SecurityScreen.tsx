import { useCallback, useEffect, useMemo, useState } from 'react';
import { ShieldAlert, ShieldCheck, TriangleAlert, type LucideIcon } from 'lucide-react';
import { toast } from 'sonner';

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
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
import FindingDetail from './security/FindingDetail.tsx';
import PolicyDialog from './security/PolicyDialog.tsx';
import {
  acknowledgeSecurityFinding,
  createSecurityPolicy,
  deleteSecurityPolicy,
  fetchSecurityFindings,
  fetchSecurityPolicies,
  fetchSecuritySummary,
  isUnauthorized,
  resolveSecurityFinding,
  suppressSecurityFinding,
  updateSecurityPolicy,
  type SecurityFinding,
  type SecurityPolicy,
  type SecurityPolicyBody,
  type SecuritySummary,
} from '@/lib/api.ts';
import { filterSecurityFindings, severityLabel } from '@/lib/security.ts';
import { timeAgo } from '@/lib/format.ts';

export function SecurityScreenView({
  summary,
  findings,
  error,
  isAdmin,
  onRetry,
  onSelect,
  onCreatePolicy,
}: {
  summary: SecuritySummary | null;
  findings: SecurityFinding[] | null;
  error: string | null;
  isAdmin: boolean;
  onRetry: () => void;
  onSelect: (finding: SecurityFinding) => void;
  onCreatePolicy: () => void;
}) {
  const [severity, setSeverity] = useState<'all' | SecurityFinding['severity']>('all');
  const [status, setStatus] = useState<'all' | SecurityFinding['status']>('all');
  const [query, setQuery] = useState('');
  const filtered = useMemo(() => filterSecurityFindings(findings ?? [], { severity, status, query }), [findings, severity, status, query]);

  if (error !== null) {
    return <ErrorState title="Couldn't load security posture" message={error} onRetry={onRetry} />;
  }
  if (summary === null || findings === null) {
    return (
      <div className="space-y-3" aria-label="Loading security findings">
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-72 w-full" />
      </div>
    );
  }
  const summaryCards: { label: string; value: number; icon: LucideIcon }[] = [
    { label: 'Active', value: summary.open, icon: ShieldAlert },
    { label: 'Critical', value: summary.critical, icon: TriangleAlert },
    { label: 'High', value: summary.high, icon: ShieldAlert },
    { label: 'Suppressed', value: summary.suppressed, icon: ShieldCheck },
  ];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-primary text-xs font-medium uppercase tracking-[0.18em]">Signals</p>
          <h1 className="text-xl font-semibold tracking-tight">Container security</h1>
          <p className="text-muted-foreground text-sm">Posture, runtime process, writable-layer, and explicit correlation findings.</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={onRetry}>Refresh</Button>
          {isAdmin && <Button onClick={onCreatePolicy}>New policy</Button>}
        </div>
      </div>

      {!summary.coverage_complete && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>Partial collector coverage</AlertTitle>
          <AlertDescription>An inspection, process, or layer collector could not complete. Existing findings remain open until a complete recovery observation arrives.</AlertDescription>
        </Alert>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {summaryCards.map(({ label, value, icon: Icon }) => (
          <Card key={label}><CardContent className="flex items-center justify-between p-4"><div><p className="text-muted-foreground text-xs">{label}</p><p className="text-2xl font-semibold">{value}</p></div><Icon className="text-muted-foreground h-5 w-5" aria-hidden /></CardContent></Card>
        ))}
      </div>

      <Card>
        <CardHeader className="gap-3">
          <CardTitle className="text-base">Finding episodes</CardTitle>
          <div className="grid gap-2 sm:grid-cols-3">
            <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search rule or container" aria-label="Search findings" />
            <Select value={severity} onValueChange={(value) => setSeverity(value as typeof severity)}>
              <SelectTrigger aria-label="Severity filter"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="all">All severities</SelectItem><SelectItem value="critical">Critical</SelectItem><SelectItem value="high">High</SelectItem><SelectItem value="medium">Medium</SelectItem><SelectItem value="low">Low</SelectItem></SelectContent>
            </Select>
            <Select value={status} onValueChange={(value) => setStatus(value as typeof status)}>
              <SelectTrigger aria-label="Status filter"><SelectValue /></SelectTrigger>
              <SelectContent><SelectItem value="all">All statuses</SelectItem><SelectItem value="open">Open</SelectItem><SelectItem value="acknowledged">Acknowledged</SelectItem><SelectItem value="resolved">Resolved</SelectItem><SelectItem value="suppressed">Suppressed</SelectItem></SelectContent>
            </Select>
          </div>
        </CardHeader>
        <CardContent className="px-0">
          {filtered.length === 0 ? (
            <div className="px-4 py-10 text-center"><ShieldCheck className="text-muted-foreground mx-auto mb-2 h-6 w-6" aria-hidden /><p className="font-medium">No security findings</p><p className="text-muted-foreground text-sm">No episodes match the current filters.</p></div>
          ) : (
            <div className="overflow-x-auto">
              <Table className="min-w-[760px]">
                <TableHeader><TableRow><TableHead>Severity</TableHead><TableHead>Rule</TableHead><TableHead>Container</TableHead><TableHead>Status</TableHead><TableHead>Last seen</TableHead><TableHead className="text-right">Occurrences</TableHead></TableRow></TableHeader>
                <TableBody>
                  {filtered.map((finding) => (
                    <TableRow key={finding.id}>
                      <TableCell><Badge variant={finding.severity === 'critical' ? 'destructive' : 'secondary'}>{finding.severity === 'critical' ? '● ' : '▲ '}{severityLabel(finding.severity)}</Badge></TableCell>
                      <TableCell><button type="button" className="text-primary text-left font-mono text-xs underline-offset-4 hover:underline focus-visible:underline" onClick={() => onSelect(finding)}>{finding.rule_id}</button></TableCell>
                      <TableCell className="max-w-48 truncate font-mono text-xs">{finding.container_id}</TableCell>
                      <TableCell><Badge variant="outline">{finding.status}</Badge></TableCell>
                      <TableCell className="text-muted-foreground text-xs">{timeAgo(finding.last_seen_at)}</TableCell>
                      <TableCell className="text-right">{finding.occurrence_count}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default function SecurityScreen({
  isAdmin,
  onUnauthorized,
  onSelectContainer,
  initialFindingId,
}: {
  isAdmin: boolean;
  onUnauthorized: () => void;
  onSelectContainer: (id: string) => void;
  initialFindingId?: number | null;
}) {
  const [summary, setSummary] = useState<SecuritySummary | null>(null);
  const [findings, setFindings] = useState<SecurityFinding[] | null>(null);
  const [policies, setPolicies] = useState<SecurityPolicy[]>([]);
  const [selected, setSelected] = useState<SecurityFinding | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [policyTarget, setPolicyTarget] = useState<SecurityPolicy | null | undefined>(undefined);
  const [policyError, setPolicyError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [nextSummary, nextFindings, nextPolicies] = await Promise.all([
        fetchSecuritySummary(), fetchSecurityFindings({ limit: 1000, sort: 'severity' }), fetchSecurityPolicies(),
      ]);
      setSummary(nextSummary);
      setFindings(nextFindings);
      setPolicies(nextPolicies);
      if (initialFindingId !== null && initialFindingId !== undefined) {
        setSelected(nextFindings.find((finding) => finding.id === initialFindingId) ?? null);
      } else {
        setSelected((current) => current === null ? null : (nextFindings.find((finding) => finding.id === current.id) ?? null));
      }
    } catch (err) {
      if (isUnauthorized(err)) { onUnauthorized(); return; }
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [initialFindingId, onUnauthorized]);

  useEffect(() => { void load(); }, [load]);

  const mutate = useCallback(async (action: () => Promise<SecurityFinding>) => {
    setBusy(true);
    try {
      const updated = await action();
      setSelected(updated);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) { onUnauthorized(); return; }
      toast.error(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }, [load, onUnauthorized]);

  async function savePolicy(scope: SecurityPolicy['scope'], scopeId: string, body: SecurityPolicyBody): Promise<void> {
    setBusy(true); setPolicyError(null);
    try {
      if (policyTarget === null) await createSecurityPolicy(scope, scopeId, body);
      else if (policyTarget !== undefined) await updateSecurityPolicy(policyTarget.id, body);
      setPolicyTarget(undefined);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) { onUnauthorized(); return; }
      setPolicyError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  if (selected !== null) {
    return <FindingDetail finding={selected} isAdmin={isAdmin} busy={busy} onClose={() => setSelected(null)} onContainer={onSelectContainer} onAcknowledge={() => void mutate(() => acknowledgeSecurityFinding(selected.id))} onResolve={() => void mutate(() => resolveSecurityFinding(selected.id))} onSuppress={(reason, until) => void mutate(() => suppressSecurityFinding(selected.id, reason, until))} />;
  }

  return (
    <div className="space-y-4">
      <SecurityScreenView summary={summary} findings={findings} error={error} isAdmin={isAdmin} onRetry={() => void load()} onSelect={setSelected} onCreatePolicy={() => setPolicyTarget(null)} />
      {policies.length > 0 && (
        <Card>
          <CardHeader><CardTitle className="text-base">Policy overrides</CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {policies.map((policy) => <div key={policy.id} className="flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm"><Badge variant="outline">{policy.scope}</Badge><span className="font-mono text-xs">{policy.scope_id || 'all containers'}</span><span className="text-muted-foreground ml-auto text-xs">Updated by {policy.updated_by}</span>{isAdmin && <><Button size="sm" variant="outline" onClick={() => setPolicyTarget(policy)}>Edit</Button><Button size="sm" variant="ghost" onClick={() => void deleteSecurityPolicy(policy.id).then(load).catch((err: unknown) => toast.error(err instanceof Error ? err.message : String(err)))}>Delete</Button></>}</div>)}
          </CardContent>
        </Card>
      )}
      <PolicyDialog open={policyTarget !== undefined} policy={policyTarget ?? null} busy={busy} error={policyError} onClose={() => setPolicyTarget(undefined)} onSubmit={(scope, scopeId, body) => void savePolicy(scope, scopeId, body)} />
    </div>
  );
}
