import type { SecurityFinding, SecuritySeverity } from './api.ts';

export interface SecurityFilters {
  severity: SecuritySeverity | 'all';
  status: SecurityFinding['status'] | 'all';
  query: string;
}

export function severityRank(value: SecuritySeverity): number {
  return { low: 1, medium: 2, high: 3, critical: 4 }[value];
}

export function filterSecurityFindings(
  findings: SecurityFinding[],
  filters: SecurityFilters,
): SecurityFinding[] {
  const query = filters.query.trim().toLowerCase();
  return findings.filter((finding) =>
    (filters.severity === 'all' || finding.severity === filters.severity) &&
    (filters.status === 'all' || finding.status === filters.status) &&
    (query === '' || finding.rule_id.toLowerCase().includes(query) ||
      finding.container_id.toLowerCase().includes(query)),
  );
}

export function countUrgentFindings(findings: SecurityFinding[]): { critical: number; high: number } {
  const active = findings.filter((finding) =>
    finding.status === 'open' || finding.status === 'acknowledged',
  );
  return {
    critical: active.filter((finding) => finding.severity === 'critical').length,
    high: active.filter((finding) => finding.severity === 'high').length,
  };
}

export function severityLabel(value: SecuritySeverity): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

export function recommendedResponse(finding: SecurityFinding): string {
  if (finding.rule_id.startsWith('correlation.')) {
    return 'Verify the executable source and deployment history, then isolate through your normal incident runbook if unauthorized.';
  }
  if (finding.category === 'process') {
    return 'Confirm the process belongs to the deployed workload and compare it with the approved image entrypoint.';
  }
  if (finding.category === 'drift') {
    return 'Compare the changed path or fingerprint with the approved release and redeploy from a trusted image if unexpected.';
  }
  return 'Review the container configuration against policy, then approve only after confirming the exception is intentional.';
}

export function notificationFindingID(body: string): number | null {
  const match = body.match(/Finding #(\d+)/i);
  if (match === null) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}
