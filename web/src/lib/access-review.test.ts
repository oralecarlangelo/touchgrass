import { describe, expect, it } from 'vitest';

import { grantOf, hostReach, matrixSections, serviceReach, unassigned } from './access-review.ts';
import type { AccessMatrix, ManagedUser } from './api.ts';

function user(
  username: string,
  role: string,
  scope: { all?: boolean; services?: string[]; disabled?: boolean },
): ManagedUser {
  return {
    id: username.length,
    username,
    role,
    all_services: scope.all === true,
    services: scope.services ?? [],
    must_change_password: false,
    disabled: scope.disabled === true,
    created_at: '2026-11-13T00:00:00Z',
  };
}

const services = [
  { id: 'billing-worker', name: 'Billing worker' },
  { id: 'docs-site', name: 'docs-site' },
];

const users = [
  user('zoe', 'viewer', { services: ['docs-site'] }),
  user('admin', 'admin', { all: true }),
  user('dana', 'developer', { services: ['billing-worker', 'docs-site'] }),
  user('abe', 'viewer', { all: true }),
  user('new-hire', 'developer', {}),
  user('gone', 'developer', { services: ['billing-worker'], disabled: true }),
];

describe('serviceReach', () => {
  it('lists, for each service, all-services accounts and the ones granted it by name', () => {
    const reach = serviceReach(services, users);

    expect(reach.map((entry) => entry.serviceId)).toEqual(['billing-worker', 'docs-site']);
    expect(reach[0].accounts.map((account) => `${account.username}:${account.role}`)).toEqual([
      'admin:admin',
      'dana:developer',
      'gone:developer',
      'abe:viewer',
    ]);
    expect(reach[1].accounts.map((account) => account.username)).toEqual(['admin', 'dana', 'abe', 'zoe']);
  });

  it('says how each account got there and whether it can sign in', () => {
    const [billing] = serviceReach(services, users);
    const byName = Object.fromEntries(billing.accounts.map((account) => [account.username, account]));

    expect(byName.admin.viaAllServices).toBe(true);
    expect(byName.abe.viaAllServices).toBe(true);
    expect(byName.dana.viaAllServices).toBe(false);
    expect(byName.gone.disabled).toBe(true);
    expect(byName.dana.disabled).toBe(false);
  });

  it('shows a service nobody was granted as reached only by all-services accounts', () => {
    const [lonely] = serviceReach([{ id: 'new-api', name: 'new-api' }], users);

    expect(lonely.accounts.map((account) => account.username)).toEqual(['admin', 'abe']);
    expect(serviceReach([{ id: 'new-api', name: 'new-api' }], [])[0].accounts).toEqual([]);
  });
});

describe('hostReach and unassigned', () => {
  it('gives the host screens to all-services accounts only', () => {
    expect(hostReach(users).map((account) => account.username)).toEqual(['admin', 'abe']);
  });

  it('finds accounts that were granted nothing', () => {
    expect(unassigned(users).map((account) => account.username)).toEqual(['new-hire']);
  });
});

const matrix: AccessMatrix = {
  roles: ['viewer', 'developer', 'admin'],
  capabilities: [
    {
      capability: 'session.self',
      scope: 'self',
      summary: 'Sign out',
      roles: { viewer: true, developer: true, admin: true },
      limits: {},
    },
    {
      capability: 'env.stage',
      scope: 'service',
      summary: 'Stage or remove an env entry',
      roles: { viewer: false, developer: true, admin: true },
      limits: { developer: 'Proposes only.' },
    },
    {
      capability: 'users.manage',
      scope: 'fleet',
      summary: 'Manage users',
      roles: { viewer: false, developer: false, admin: true },
      limits: {},
    },
    {
      capability: 'logs.read',
      scope: 'service',
      summary: 'Search logs',
      roles: { viewer: true, developer: true, admin: true },
      limits: {},
    },
  ],
};

describe('matrixSections', () => {
  it('groups capabilities by scope and keeps the server order', () => {
    const sections = matrixSections(matrix);

    expect(sections.map((section) => section.scope)).toEqual(['self', 'service', 'fleet']);
    expect(sections[1].rows.map((row) => row.capability)).toEqual(['env.stage', 'logs.read']);
    expect(sections[2].explanation).toContain('all services');
  });

  it('still shows a scope this build does not know', () => {
    const [section] = matrixSections({
      roles: ['viewer'],
      capabilities: [{ capability: 'x.y', scope: 'team', summary: 'X', roles: {}, limits: {} }],
    });

    expect(section.title).toBe('team');
  });
});

describe('grantOf', () => {
  it('tells a full grant from a limited one and from none', () => {
    const stage = matrix.capabilities[1];

    expect(grantOf(stage, 'admin')).toBe('yes');
    expect(grantOf(stage, 'developer')).toBe('limited');
    expect(grantOf(stage, 'viewer')).toBe('no');
    expect(grantOf(stage, 'owner')).toBe('no');
  });
});
