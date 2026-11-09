import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { EnvironmentScreenView, envRowSummary, type EnvFleetRow } from './EnvironmentScreen.tsx';
import type { EnvOverview, ServiceView } from '@/lib/api.ts';

const noop = () => undefined;
const service: ServiceView = { id: 'shop-api', name: 'Shop API', strategy: 'recreate', live_color: '', health: 'healthy', colors: [], containers: [] };
const overview: EnvOverview = {
  configured: true,
  target: { path: '/srv/shop-api/.env', owner: 'app:app', mode: '0600' },
  entries: [
    { key: 'A', is_secret: false, value: '1', masked: false, updated_by: '', updated_at: '2026-11-10T00:00:00Z', state: 'applied', required: false },
    { key: 'B', is_secret: true, value: null, masked: true, updated_by: '', updated_at: '2026-11-10T00:00:00Z', state: 'applied', required: false },
  ],
  applied_revision: { id: 'r', created_at: '2026-11-09T00:00:00Z', created_by: 'ana', summary: '' },
  pending_count: 0,
  last_validation: null,
  apply_in_progress: false,
};

describe('EnvironmentScreen', () => {
  it('summarises each service state', () => {
    const row = (patch: Partial<EnvFleetRow>): EnvFleetRow => ({ service, overview, error: null, ...patch });

    expect(envRowSummary(row({ error: 'offline' }))).toMatchObject({ tone: 'error' });
    expect(envRowSummary(row({ overview: null }))).toMatchObject({ label: 'Loading…' });
    expect(envRowSummary(row({ overview: { ...overview, configured: false, reason: 'no_target' } }))).toMatchObject({ label: 'No env target configured' });
    expect(envRowSummary(row({ overview: { ...overview, configured: false, reason: 'key_missing' } }))).toMatchObject({ label: 'No encryption key on the host' });
    expect(envRowSummary(row({ overview: { ...overview, apply_in_progress: true } }))).toMatchObject({ label: 'Apply running' });
    expect(envRowSummary(row({ overview: { ...overview, pending_count: 2 } }))).toMatchObject({ label: '2 changes not applied', tone: 'pending' });
    expect(envRowSummary(row({ overview: { ...overview, entries: [] } }))).toMatchObject({ label: 'No entries yet' });
    expect(envRowSummary(row({}))).toMatchObject({ label: 'Up to date', tone: 'ok' });
  });

  it('renders loading, empty and populated views with a per-service link', () => {
    expect(renderToStaticMarkup(<EnvironmentScreenView rows={null} loading isAdmin onRefresh={noop} onOpenService={noop} />)).toContain('Loading environment status');
    expect(renderToStaticMarkup(<EnvironmentScreenView rows={[]} loading={false} isAdmin onRefresh={noop} onOpenService={noop} />)).toContain('No services yet');

    const admin = renderToStaticMarkup(
      <EnvironmentScreenView rows={[{ service, overview, error: null }]} loading={false} isAdmin onRefresh={noop} onOpenService={noop} />,
    );
    const viewer = renderToStaticMarkup(
      <EnvironmentScreenView rows={[{ service, overview, error: null }]} loading={false} isAdmin={false} onRefresh={noop} onOpenService={noop} />,
    );
    expect(admin).toContain('Shop API');
    expect(admin).toContain('/srv/shop-api/.env');
    expect(admin).toContain('2 keys');
    expect(admin).toContain('Up to date');
    expect(admin).toContain('aria-label="Open environment for Shop API"');
    expect(admin).toContain('Manage');
    expect(viewer).toContain('View');
    expect(viewer).not.toContain('Manage');
  });
});
