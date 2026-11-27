import { useCallback, useState, type ReactNode } from 'react';
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
import CollectorStatus from './performance/CollectorStatus.tsx';
import FindingDetail from './security/FindingDetail.tsx';
import PolicyDialog from './security/PolicyDialog.tsx';
import {
  acknowledgeSecurityFinding,
  createSecurityPolicy,
  deleteSecurityPolicy,
  fetchSecurityFinding,
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
import { findingCollector, severityLabel } from '@/lib/security.ts';
import { timeAgo } from '@/lib/format.ts';
import { usePanelRead } from '@/lib/use-panel-read.ts';

const pageSize = 50;
type FindingSource = 'workload' | 'collector';

export function SecurityScreenView({
  summary,
  findings,
  error,
  summaryError = null,
  onRetrySummary,
  isAdmin,
  onRetry,
  onSelect,
  onCreatePolicy,
  source = 'workload',
  controls,
  pagination,
  onRetryFindings,
}: {
  summary: SecuritySummary | null;
  findings: SecurityFinding[] | null;
  error: string | null;
  summaryError?: string | null;
  onRetrySummary?: () => void;
  isAdmin: boolean;
  onRetry: () => void;
  onSelect: (finding: SecurityFinding) => void;
  onCreatePolicy: () => void;
  source?: FindingSource;
  controls?: ReactNode;
  pagination?: ReactNode;
  onRetryFindings?: () => void;
}) {
  const summaryCards: { label: string; value: number; icon: LucideIcon }[] = summary === null ? [] : [
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

      {summaryError !== null && (
        <ErrorState title="Couldn’t refresh collector coverage" message={summaryError} onRetry={onRetrySummary ?? onRetry} />
      )}
      {summary === null && summaryError === null && <Skeleton className="h-24 w-full" aria-label="Loading collector coverage" />}

      {summary?.coverage_complete === false && (
        <Alert variant="destructive">
          <TriangleAlert aria-hidden />
          <AlertTitle>Partial collector coverage</AlertTitle>
          <AlertDescription>An enabled collector is waiting for data, stale, or degraded. Existing findings remain open until a complete recovery observation arrives.</AlertDescription>
        </Alert>
      )}

      {summary?.collectors && summary.collectors.length > 0 && (
        <details className="bg-card rounded-lg border" open={!summary.coverage_complete || undefined}>
          <summary className="cursor-pointer p-4 text-sm font-medium">Security collector coverage</summary>
          <CollectorStatus collectors={summary.collectors} services={[]} serviceId="" fleetContainers={[]} />
        </details>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {summaryCards.map(({ label, value, icon: Icon }) => (
          <Card key={label}><CardContent className="flex items-center justify-between p-4"><div><p className="text-muted-foreground text-xs">{label}</p><p className="text-2xl font-semibold">{value}</p></div><Icon className="text-muted-foreground h-5 w-5" aria-hidden /></CardContent></Card>
        ))}
      </div>

      <Card>
        <CardHeader className="gap-3">
          <CardTitle className="text-base">{source === 'collector' ? 'Collector incidents' : 'Workload findings'}</CardTitle>
          {controls}
        </CardHeader>
        <CardContent className="px-0">
          {error !== null && (
            <div className="px-4"><ErrorState title="Couldn’t refresh findings" message={error} onRetry={onRetryFindings ?? onRetry} /></div>
          )}
          {findings === null ? (
            error === null && <Skeleton className="m-4 h-40" aria-label="Loading security findings" />
          ) : findings.length === 0 ? (
            <div className="px-4 py-10 text-center"><ShieldCheck className="text-muted-foreground mx-auto mb-2 h-6 w-6" aria-hidden /><p className="font-medium">{source === 'collector' ? 'No collector incidents' : 'No security findings'}</p><p className="text-muted-foreground text-sm">No episodes match the current filters.</p></div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader><TableRow><TableHead>Rule</TableHead><TableHead className="hidden md:table-cell">{source === 'collector' ? 'Collector' : 'Container'}</TableHead><TableHead>Severity</TableHead><TableHead className="hidden sm:table-cell">Status</TableHead><TableHead className="hidden lg:table-cell">Last seen</TableHead><TableHead className="hidden text-right lg:table-cell">Occurrences</TableHead></TableRow></TableHeader>
                <TableBody>
                  {findings.map((finding) => (
                    <TableRow key={finding.id}>
                      <TableCell className="whitespace-normal">
                        <button type="button" className="text-primary break-all text-left font-mono text-xs underline-offset-4 hover:underline focus-visible:underline" onClick={() => onSelect(finding)}>{finding.rule_id}</button>
                        <div className="text-muted-foreground text-xs md:hidden break-all">{findingCollector(finding) ?? finding.container_id}</div>
                        <div className="text-muted-foreground text-xs lg:hidden">
                          <span className="sm:hidden">{finding.status} · </span>{finding.occurrence_count} occurrences
                        </div>
                      </TableCell>
                      <TableCell className="hidden max-w-48 truncate font-mono text-xs md:table-cell">{findingCollector(finding) ?? finding.container_id}</TableCell>
                      <TableCell><Badge variant={finding.severity === 'critical' ? 'destructive' : 'secondary'}>{finding.severity === 'critical' ? '● ' : '▲ '}{severityLabel(finding.severity)}</Badge></TableCell>
                      <TableCell className="hidden sm:table-cell"><Badge variant="outline">{finding.status}</Badge></TableCell>
                      <TableCell className="text-muted-foreground hidden text-xs lg:table-cell">{timeAgo(finding.last_seen_at)}</TableCell>
                      <TableCell className="hidden text-right lg:table-cell">{finding.occurrence_count}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          {pagination}
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
  const [refresh, setRefresh] = useState(0);
  const [selected, setSelected] = useState<SecurityFinding | null>(null);
  const [dismissedFinding, setDismissedFinding] = useState<number | null>(null);
  const [policiesOpen, setPoliciesOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [policyTarget, setPolicyTarget] = useState<SecurityPolicy | null | undefined>(undefined);
  const [policyError, setPolicyError] = useState<string | null>(null);
  const [source, setSource] = useState<FindingSource>('workload');
  const [severity, setSeverity] = useState<'all' | SecurityFinding['severity']>('all');
  const [status, setStatus] = useState<'all' | SecurityFinding['status']>('open');
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState('');
  const [page, setPage] = useState(0);

  const readFindings = useCallback((signal: AbortSignal) => fetchSecurityFindings({
    source, q: query, limit: pageSize + 1, offset: page * pageSize, sort: 'severity',
    severity: severity === 'all' ? undefined : severity,
    status: status === 'all' ? undefined : status,
  }, signal), [source, query, page, severity, status]);
  const linkedId = initialFindingId !== dismissedFinding ? initialFindingId : null;
  const readLinkedFinding = useCallback(
    (signal: AbortSignal) => fetchSecurityFinding(linkedId ?? 0, signal), [linkedId],
  );

  const summary = usePanelRead('fleet', fetchSecuritySummary, refresh, onUnauthorized);
  const findings = usePanelRead(JSON.stringify([source, query, page, severity, status]), readFindings, refresh, onUnauthorized);
  const policies = usePanelRead('fleet', policiesOpen ? fetchSecurityPolicies : null, refresh, onUnauthorized);
  const linked = usePanelRead(String(linkedId), linkedId != null ? readLinkedFinding : null, refresh, onUnauthorized);
  const load = useCallback(() => setRefresh((value) => value + 1), []);
  const activeFinding = selected ?? linked.data;
  const closeFinding = () => {
    if (linkedId != null && activeFinding?.id === linkedId) setDismissedFinding(linkedId);
    setSelected(null);
  };

  const mutate = useCallback(async (action: () => Promise<SecurityFinding>) => {
    setBusy(true);
    try {
      const updated = await action();
      setSelected(updated);
      load();
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
      load();
    } catch (err) {
      if (isUnauthorized(err)) { onUnauthorized(); return; }
      setPolicyError(err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  }

  if (activeFinding !== null) {
    return <FindingDetail finding={activeFinding} isAdmin={isAdmin} busy={busy} onClose={closeFinding} onContainer={onSelectContainer} onAcknowledge={() => void mutate(() => acknowledgeSecurityFinding(activeFinding.id))} onResolve={() => void mutate(() => resolveSecurityFinding(activeFinding.id))} onSuppress={(reason, until) => void mutate(() => suppressSecurityFinding(activeFinding.id, reason, until))} />;
  }

  return (
    <div className="space-y-4">
      {linked.error !== null && (
        <ErrorState title="Couldn’t open the linked finding" message={linked.error} onRetry={linked.retry} />
      )}
      <SecurityScreenView
        summary={summary.data}
        summaryError={summary.error}
        onRetrySummary={summary.retry}
        findings={findings.data?.slice(0, pageSize) ?? null}
        error={findings.error}
        isAdmin={isAdmin}
        onRetry={load}
        onRetryFindings={findings.retry}
        onSelect={setSelected}
        onCreatePolicy={() => setPolicyTarget(null)}
        source={source}
        controls={(
          <>
            <div role="group" aria-label="Finding source" className="flex flex-wrap gap-2">
              <Button variant={source === 'workload' ? 'secondary' : 'ghost'} aria-pressed={source === 'workload'} onClick={() => { setSource('workload'); setPage(0); }}>Workload findings</Button>
              <Button variant={source === 'collector' ? 'secondary' : 'ghost'} aria-pressed={source === 'collector'} onClick={() => { setSource('collector'); setPage(0); }}>Collector incidents</Button>
            </div>
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-[2fr_1fr_1fr]">
              <form role="search" className="flex gap-2 sm:col-span-2 lg:col-span-1" onSubmit={(event) => { event.preventDefault(); setQuery(searchInput.trim()); setPage(0); }}>
                <Input value={searchInput} onChange={(event) => setSearchInput(event.target.value)} maxLength={128} placeholder="Search rule or container" aria-label="Search findings" />
                <Button type="submit" variant="outline">Search</Button>
              </form>
              <Select value={severity} onValueChange={(value) => { setSeverity(value as typeof severity); setPage(0); }}>
                <SelectTrigger aria-label="Severity filter"><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="all">All severities</SelectItem><SelectItem value="critical">Critical</SelectItem><SelectItem value="high">High</SelectItem><SelectItem value="medium">Medium</SelectItem><SelectItem value="low">Low</SelectItem></SelectContent>
              </Select>
              <Select value={status} onValueChange={(value) => { setStatus(value as typeof status); setPage(0); }}>
                <SelectTrigger aria-label="Status filter"><SelectValue /></SelectTrigger>
                <SelectContent><SelectItem value="all">All statuses</SelectItem><SelectItem value="open">Open</SelectItem><SelectItem value="acknowledged">Acknowledged</SelectItem><SelectItem value="resolved">Resolved</SelectItem><SelectItem value="suppressed">Suppressed</SelectItem></SelectContent>
              </Select>
            </div>
          </>
        )}
        pagination={(page > 0 || (findings.data?.length ?? 0) > pageSize) && (
          <nav className="flex items-center gap-3 px-4 pt-3" aria-label="Finding pages">
            <Button size="sm" variant="outline" disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</Button>
            <span className="text-muted-foreground text-sm" aria-live="polite">Page {page + 1}</span>
            <Button size="sm" variant="outline" disabled={(findings.data?.length ?? 0) <= pageSize} onClick={() => setPage(page + 1)}>Next</Button>
          </nav>
        )}
      />
      <details className="bg-card rounded-lg border" onToggle={(event) => setPoliciesOpen(event.currentTarget.open)}>
        <summary className="cursor-pointer p-4 text-sm font-medium">Policy overrides</summary>
        <div className="space-y-2 px-4 pb-4">
          {policies.error !== null && <ErrorState title="Couldn’t refresh policies" message={policies.error} onRetry={policies.retry} />}
          {policies.loading && policies.data === null && <p role="status">Loading policies…</p>}
          {policies.data?.length === 0 && <p className="text-muted-foreground text-sm">No policy overrides.</p>}
          {policies.data?.map((policy) => <div key={policy.id} className="flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm"><Badge variant="outline">{policy.scope}</Badge><span className="font-mono text-xs">{policy.scope_id || 'all containers'}</span><span className="text-muted-foreground ml-auto text-xs">Updated by {policy.updated_by}</span>{isAdmin && <><Button size="sm" variant="outline" onClick={() => setPolicyTarget(policy)}>Edit</Button><Button size="sm" variant="ghost" onClick={() => void deleteSecurityPolicy(policy.id).then(load).catch((err: unknown) => toast.error(err instanceof Error ? err.message : String(err)))}>Delete</Button></>}</div>)}
        </div>
      </details>
      <PolicyDialog open={policyTarget !== undefined} policy={policyTarget ?? null} busy={busy} error={policyError} onClose={() => setPolicyTarget(undefined)} onSubmit={(scope, scopeId, body) => void savePolicy(scope, scopeId, body)} />
    </div>
  );
}
