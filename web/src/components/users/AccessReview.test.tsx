import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { RoleMatrixTable, ServiceReachTable } from './AccessReview.tsx';
import type { AccessMatrix, ManagedUser } from '@/lib/api.ts';

const services = [
  { id: 'billing-worker', name: 'Billing worker' },
  { id: 'docs-site', name: 'docs-site' },
];

function user(username: string, role: string, all: boolean, granted: string[], disabled = false): ManagedUser {
  return {
    id: username.length,
    username,
    role,
    all_services: all,
    services: granted,
    must_change_password: false,
    disabled,
    created_at: '2026-11-13T00:00:00Z',
  };
}

describe('ServiceReachTable', () => {
  it('names who reaches each service, with the role and how', () => {
    const html = renderToStaticMarkup(
      <ServiceReachTable
        services={services}
        users={[
          user('admin', 'admin', true, []),
          user('dana', 'developer', false, ['billing-worker']),
          user('gone', 'viewer', false, ['docs-site'], true),
          user('new-hire', 'viewer', false, []),
        ]}
      />,
    );

    expect(html).toContain('Billing worker');
    expect(html).toContain('billing-worker');
    expect(html).toContain('dana: developer, granted this service.');
    expect(html).toContain('admin: admin, has every service.');
    expect(html).toContain('gone: viewer, granted this service. Disabled: cannot sign in.');
    expect(html).toContain('Host screens');
    expect(html).toContain('No services assigned');
    expect(html).toContain('new-hire');
  });

  it('says so when a service or the host has nobody', () => {
    const html = renderToStaticMarkup(
      <ServiceReachTable services={services} users={[user('dana', 'developer', false, ['docs-site'])]} />,
    );

    expect(html.match(/Nobody/g)?.length).toBe(2);
    expect(html).not.toContain('No services assigned');
  });
});

const matrix: AccessMatrix = {
  roles: ['viewer', 'developer', 'admin'],
  capabilities: [
    {
      capability: 'env.stage',
      scope: 'service',
      summary: 'Stage or remove an env entry',
      roles: { viewer: false, developer: true, admin: true },
      limits: { developer: 'Proposes only: an admin applies.' },
    },
    {
      capability: 'users.manage',
      scope: 'fleet',
      summary: 'Manage users and their access',
      roles: { viewer: false, developer: false, admin: true },
      limits: {},
    },
  ],
};

describe('RoleMatrixTable', () => {
  it('renders the server matrix with summaries, names and limits', () => {
    const html = renderToStaticMarkup(<RoleMatrixTable matrix={matrix} />);

    expect(html).toContain('Per service');
    expect(html).toContain('Whole host');
    expect(html).toContain('Stage or remove an env entry');
    expect(html).toContain('env.stage');
    expect(html).toContain('Proposes only: an admin applies.');
    expect(html).toContain('aria-label="developer: Limited"');
    expect(html).toContain('aria-label="viewer: no"');
    expect(html).toContain('aria-label="admin: Yes"');
  });

  it('shows nothing it was not sent', () => {
    const html = renderToStaticMarkup(<RoleMatrixTable matrix={{ roles: ['viewer'], capabilities: [] }} />);

    expect(html).not.toContain('<table');
  });
});
