import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { SecurityScreenView } from './SecurityScreen.tsx';
import type { SecurityFinding, SecuritySummary } from '@/lib/api.ts';

const summary: SecuritySummary = { open: 0, critical: 0, high: 0, suppressed: 0, coverage_complete: true };
const finding: SecurityFinding = {
  id: 1, fingerprint: 'safe-fingerprint', episode: 1, container_id: 'container',
  rule_id: 'posture.docker_socket', category: 'posture', severity: 'critical', confidence: 'high',
  status: 'open', occurrence_count: 1, evidence: { docker_socket: 'true' },
  opened_at: '2026-10-29T00:00:00Z', last_seen_at: '2026-10-29T00:00:00Z',
};

describe('SecurityScreenView', () => {
  it('renders loading, error, empty, and partial coverage states', () => {
    expect(renderToStaticMarkup(<SecurityScreenView summary={null} findings={null} error={null} isAdmin={false} onRetry={() => undefined} onSelect={() => undefined} onCreatePolicy={() => undefined} />)).toContain('Loading security findings');
    expect(renderToStaticMarkup(<SecurityScreenView summary={null} findings={null} error="offline" isAdmin={false} onRetry={() => undefined} onSelect={() => undefined} onCreatePolicy={() => undefined} />)).toContain('offline');
    expect(renderToStaticMarkup(<SecurityScreenView summary={summary} findings={[]} error={null} isAdmin={false} onRetry={() => undefined} onSelect={() => undefined} onCreatePolicy={() => undefined} />)).toContain('No security findings');
    expect(renderToStaticMarkup(<SecurityScreenView summary={{ ...summary, coverage_complete: false }} findings={[finding]} error={null} isAdmin={false} onRetry={() => undefined} onSelect={() => undefined} onCreatePolicy={() => undefined} />)).toContain('Partial collector coverage');
  });

  it('renders a narrow-screen-safe table and admin-only policy action', () => {
    const viewer = renderToStaticMarkup(<SecurityScreenView summary={summary} findings={[finding]} error={null} isAdmin={false} onRetry={() => undefined} onSelect={() => undefined} onCreatePolicy={() => undefined} />);
    const admin = renderToStaticMarkup(<SecurityScreenView summary={summary} findings={[finding]} error={null} isAdmin onRetry={() => undefined} onSelect={() => undefined} onCreatePolicy={() => undefined} />);
    expect(viewer).toContain('overflow-x-auto');
    expect(viewer).not.toContain('New policy');
    expect(admin).toContain('New policy');
    expect(viewer).not.toContain('raw_command');
  });
});
