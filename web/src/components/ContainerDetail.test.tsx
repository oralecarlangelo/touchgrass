import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { ContainerDetailView } from './ContainerDetail.tsx';
import type { ContainerDetailResponse } from '@/lib/api.ts';

describe('ContainerDetailView', () => {
  it('renders stopped containers with safe posture data only', () => {
    const detail: ContainerDetailResponse = {
      container: {
        id: 'new-id', name: 'api-1', project: 'app', managed: false,
        state: 'exited', classification: 'unknown', current: true, present: true,
        health: 'exited', cpu_percent: null, mem_bytes: null, mem_limit: null,
        restarts: null, risk_counts: { critical: 1, high: 0, medium: 0, low: 0 },
      },
      image: 'app:latest', image_id: 'sha256:image',
      inspection: {
        user: '', privileged: true, readonly_root_fs: false, cap_add: [],
        mounts: [{ type: 'bind', destination: '/data', writable: true }],
        has_docker_socket: false, command_hash: 'safe-hash',
      },
    };
    const html = renderToStaticMarkup(<ContainerDetailView detail={detail} />);

    expect(html).toContain('exited');
    expect(html).toContain('safe-hash');
    expect(html).toContain('/data');
    expect(html).not.toContain('environment');
    expect(html).not.toContain('mount source');
  });
});
