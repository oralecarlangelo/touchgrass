import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import Dashboard, { collectionFreshnessLabel, containerPostureLabel, settleRegion } from './Dashboard.tsx';
import type { FleetContainerSummary } from '@/lib/api.ts';

function fleet(stale: boolean, critical: number): FleetContainerSummary {
  return {
    freshness: { reconciled_at: '2026-10-29T00:00:00Z', complete: true, stale, observed_count: 1 },
    total: 1,
    critical,
    unknown: 1,
    stopped: 0,
  };
}

describe('containerPostureLabel', () => {
  it('never summarizes a critical unknown container as healthy', () => {
    expect(containerPostureLabel(fleet(false, 1))).toBe('1 critical');
  });

  it('shows stale collection as degraded', () => {
    expect(containerPostureLabel(fleet(true, 0))).toBe('degraded collection');
  });
});

describe('settleRegion', () => {
  it('applies a fast region without waiting for another request', async () => {
    let resolveSlow!: (value: string) => void;
    const slow = new Promise<string>((resolve) => { resolveSlow = resolve; });
    const applied: string[] = [];

    const slowDone = settleRegion(slow, () => true, (value) => applied.push(value), () => undefined, () => undefined);
    await settleRegion(Promise.resolve('notes'), () => true, (value) => applied.push(value), () => undefined, () => undefined);

    expect(applied).toEqual(['notes']);
    resolveSlow('system');
    await slowDone;
    expect(applied).toEqual(['notes', 'system']);
  });
});

describe('collectionFreshnessLabel', () => {
  it('identifies cached degraded data and its observation time', () => {
    expect(collectionFreshnessLabel({
      observed_at: new Date(Date.now() - 60_000).toISOString(),
      complete: false,
      stale: true,
      last_error_category: 'collection_failed',
    })).toContain('Degraded · observed');
    expect(collectionFreshnessLabel({ observed_at: null, complete: false, stale: true })).toBe('Degraded · never observed');
  });
});

describe('Dashboard services region', () => {
  it('shows a retryable error instead of an endless skeleton', () => {
    const html = renderToStaticMarkup(createElement(Dashboard, {
      services: null,
      servicesError: 'HTTP 502',
      servicesRefreshing: false,
      servicesUpdatedAt: null,
      servicesFreshness: null,
      onRetryServices: () => undefined,
      onSelectService: () => undefined,
      onView: () => undefined,
      onUnauthorized: () => undefined,
      canHost: false,
    }));

    expect(html).toContain('Services unavailable');
    expect(html).toContain('Retry');
    expect(html).not.toContain('aria-label="Loading managed services"');
  });

  it('keeps last-known services visible when refresh fails', () => {
    const html = renderToStaticMarkup(createElement(Dashboard, {
      services: [{
        id: 'docs', name: 'Docs', strategy: 'recreate', live_color: '', health: 'healthy', colors: [], containers: [],
      }],
      servicesError: 'HTTP 502',
      servicesRefreshing: false,
      servicesUpdatedAt: new Date().toISOString(),
      servicesFreshness: null,
      onRetryServices: () => undefined,
      onSelectService: () => undefined,
      onView: () => undefined,
      onUnauthorized: () => undefined,
      canHost: false,
    }));

    expect(html).toContain('Docs');
    expect(html).toContain('Refresh failed');
  });

  it('shows degraded source freshness', () => {
    const observedAt = new Date(Date.now() - 60_000).toISOString();
    const html = renderToStaticMarkup(createElement(Dashboard, {
      services: [],
      servicesError: null,
      servicesRefreshing: false,
      servicesUpdatedAt: observedAt,
      servicesFreshness: { observed_at: observedAt, complete: false, stale: true },
      onRetryServices: () => undefined,
      onSelectService: () => undefined,
      onView: () => undefined,
      onUnauthorized: () => undefined,
      canHost: false,
    }));

    expect(html).toContain('Degraded');
    expect(html).toContain('observed');
  });
});
