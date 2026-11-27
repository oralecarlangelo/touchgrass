import type { SecurityFinding, SecuritySeverity } from './api.ts';

export function severityRank(value: SecuritySeverity): number {
  return { low: 1, medium: 2, high: 3, critical: 4 }[value];
}

export function findingCollector(finding: SecurityFinding): string | null {
  switch (finding.container_id) {
    case 'collector:security_posture': return 'Container inspection';
    case 'collector:security_process': return 'Runtime process scan';
    case 'collector:security_diff': return 'Writable-layer scan';
    default: return null;
  }
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
  if (findingCollector(finding) !== null) {
    return 'Check the collector error and last successful sample in Performance. Open workload findings stay active until coverage recovers.';
  }
  if (finding.rule_id.startsWith('correlation.')) {
    return 'Verify the executable source and deployment history, then isolate through your normal incident runbook if unauthorized.';
  }
  if (finding.category === 'process') {
    return 'Confirm the process belongs to the deployed workload and compare it with the approved image entrypoint.';
  }
  if (finding.category === 'drift') {
    return 'Compare the changed path or fingerprint with the approved release and redeploy from a trusted image if unexpected.';
  }
  if (finding.category === 'traffic') {
    return 'Confirm the edge control blocked the request, review its occurrence trend, and escalate only if the traffic bypassed the control or affected availability.';
  }
  return 'Review the container configuration against policy, then approve only after confirming the exception is intentional.';
}

export function notificationFindingID(body: string): number | null {
  const match = body.match(/Finding #(\d+)/i);
  if (match === null) return null;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : null;
}
