import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import FindingDetail from './FindingDetail.tsx';
import type { SecurityFinding } from '@/lib/api.ts';

const finding: SecurityFinding = {
  id: 7, fingerprint: 'fingerprint', episode: 1, container_id: 'container-7',
  rule_id: 'correlation.added_executable_running', category: 'drift', severity: 'critical',
  confidence: 'high', status: 'open', occurrence_count: 2,
  evidence: { path: '/tmp/update', contributing_rules: 'diff.added_executable,process.high_risk_path' },
  opened_at: '2026-10-29T00:00:00Z', last_seen_at: '2026-10-29T00:01:00Z',
};

describe('FindingDetail', () => {
  it('is keyboard reachable, explains correlation, and keeps viewers read-only', () => {
    const html = renderToStaticMarkup(<FindingDetail finding={finding} isAdmin={false} busy={false} onClose={() => undefined} onContainer={() => undefined} onAcknowledge={() => undefined} onResolve={() => undefined} onSuppress={() => undefined} />);
    expect(html).toContain('aria-label="Security finding detail"');
    expect(html).toContain('diff.added_executable');
    expect(html).toContain('/tmp/update');
    expect(html).toContain('Read-only');
    expect(html).not.toContain('Acknowledge');
  });

  it('shows admin workflow controls without containment actions', () => {
    const html = renderToStaticMarkup(<FindingDetail finding={finding} isAdmin busy={false} onClose={() => undefined} onContainer={() => undefined} onAcknowledge={() => undefined} onResolve={() => undefined} onSuppress={() => undefined} />);
    expect(html).toContain('Acknowledge');
    expect(html).toContain('Resolve');
    expect(html).toContain('Suppress');
    expect(html).not.toContain('Kill container');
  });
});
