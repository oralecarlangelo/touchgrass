import { afterEach, describe, expect, it, vi } from 'vitest';

import { fetchFleetContainerPage, fetchStorageUsage, fetchSystemHistory, suggestService } from './api.ts';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('abortable fleet requests', () => {
  it('makes one bounded inventory request with the current filters', async () => {
    const controller = new AbortController();
    const freshness = { reconciled_at: null, stale: false, complete: false, observed_count: 0 };
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ containers: [], freshness })));
    vi.stubGlobal('fetch', fetchMock);

    const page = await fetchFleetContainerPage({
      offset: 40, limit: 41, sort: 'project', filter: 'stopped', search: 'sample & worker',
    }, controller.signal);

    expect(page).toEqual({ containers: [], freshness });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/system/containers?limit=41&offset=40&sort=project&filter=stopped&q=sample+%26+worker',
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('loads all history series with one abortable request', async () => {
    const controller = new AbortController();
    const series = { cpu: [], mem: [], load: [] };
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ series })));
    vi.stubGlobal('fetch', fetchMock);

    expect(await fetchSystemHistory(24, controller.signal)).toEqual(series);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/system/history?metric=all&hours=24',
      expect.objectContaining({ signal: controller.signal }),
    );
  });

  it('forwards the caller signal to onboarding suggestions', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      container: 'web',
      service_id: 'web',
      strategy: 'recreate',
      compose_project: 'example',
      compose_dir: '/srv/example',
      service: 'web',
      health_url: '',
      public_url: '',
      deploy_script: 'deploy.sh',
      rollback_script: 'rollback.sh',
      confidence: 'medium',
    })));
    vi.stubGlobal('fetch', fetchMock);

    const suggestion = await suggestService('web', controller.signal, false);

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/onboarding/suggest?container=web&probe=false',
      expect.objectContaining({ signal: controller.signal }),
    );
    expect(suggestion.reasons).toEqual([]);
    expect(suggestion.warnings).toEqual([]);
  });

  it('forwards the caller signal to storage usage', async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({})));
    vi.stubGlobal('fetch', fetchMock);

    await fetchStorageUsage(controller.signal);

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/storage/usage',
      expect.objectContaining({ signal: controller.signal }),
    );
  });
});
