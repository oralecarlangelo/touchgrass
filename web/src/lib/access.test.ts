import { describe, expect, it } from 'vitest';

import {
  accessLabel,
  canOpenView,
  describeScope,
  firstAllowedView,
  hasNoServices,
  isScoped,
  makeCan,
  needsAdmin,
  scopeLabel,
} from './access.ts';
import type { SessionInfo } from './api.ts';
import { navGroups } from './views.ts';

const viewerCapabilities = [
  'session.self',
  'service.read',
  'metrics.read',
  'deploy.read',
  'logs.read',
  'issues.read',
  'alerts.read',
  'env.read_keys',
  'keys.read',
  'audit.read',
];

const fleetReads = ['audit.read_all', 'host.read', 'security.read', 'databases.read'];

const scoped: SessionInfo = {
  authenticated: true,
  username: 'dev',
  role: 'viewer',
  all_services: false,
  services: ['billing-worker'],
  capabilities: viewerCapabilities,
};

const everywhere: SessionInfo = {
  authenticated: true,
  username: 'reader',
  role: 'viewer',
  all_services: true,
  services: [],
  capabilities: [...viewerCapabilities, ...fleetReads],
};

const admin: SessionInfo = {
  authenticated: true,
  username: 'admin',
  role: 'admin',
  all_services: true,
  services: [],
  capabilities: [
    ...viewerCapabilities,
    ...fleetReads,
    'deploy.trigger',
    'env.apply',
    'users.manage',
    'services.manage',
    'keys.manage',
  ],
};

const unassigned: SessionInfo = {
  authenticated: true,
  username: 'newbie',
  role: 'viewer',
  all_services: false,
  services: [],
  capabilities: ['session.self'],
};

function visibleViews(session: SessionInfo): string[] {
  const can = makeCan(session);

  return navGroups
    .flatMap((group) => group.items)
    .filter((tab) => canOpenView(tab.id, session, can))
    .map((tab) => tab.id);
}

describe('makeCan', () => {
  it('limits a scoped user to the services they were granted', () => {
    const can = makeCan(scoped);

    expect(can('logs.read')).toBe(true);
    expect(can('logs.read', 'billing-worker')).toBe(true);
    expect(can('logs.read', 'docs-site')).toBe(false);
    expect(can('deploy.trigger', 'billing-worker')).toBe(false);
    expect(can('host.read')).toBe(false);
  });

  it('lets an all-services user reach any service', () => {
    const can = makeCan(everywhere);

    expect(can('logs.read', 'docs-site')).toBe(true);
    expect(can('host.read')).toBe(true);
    expect(can('deploy.trigger', 'docs-site')).toBe(false);
    expect(makeCan(admin)('deploy.trigger', 'docs-site')).toBe(true);
  });

  it('grants nothing without a session or without capabilities', () => {
    expect(makeCan(null)('session.self')).toBe(false);
    expect(makeCan({ authenticated: false, username: '', role: '' })('service.read')).toBe(false);
    expect(makeCan({ authenticated: true, username: 'old', role: 'admin' })('users.manage')).toBe(false);
  });
});

describe('navigation', () => {
  it('shows a scoped viewer only the screens their services can fill', () => {
    expect(visibleViews(scoped)).toEqual([
      'dashboard',
      'services',
      'performance',
      'issues',
      'logs',
      'notifications',
      'environment',
      'rules',
      'audit',
    ]);
  });

  it('keeps every screen but Users for an all-services viewer', () => {
    const views = visibleViews(everywhere);

    expect(views).toContain('fleet');
    expect(views).toContain('security');
    expect(views).toContain('databases');
    expect(views).toContain('images');
    expect(views).toContain('keys');
    expect(views).not.toContain('users');
    expect(visibleViews(admin)).toContain('users');
  });

  it('tells a role refusal from a scope refusal', () => {
    expect(needsAdmin('users')).toBe(true);
    expect(needsAdmin('fleet')).toBe(false);
    expect(needsAdmin('logs')).toBe(false);
  });

  it('shows nothing to a viewer with no services, but Help stays open', () => {
    expect(visibleViews(unassigned)).toEqual([]);
    expect(canOpenView('help', unassigned, makeCan(unassigned))).toBe(true);
    expect(firstAllowedView(unassigned, makeCan(unassigned))).toBe('help');
    expect(firstAllowedView(scoped, makeCan(scoped))).toBe('dashboard');
  });
});

describe('labels', () => {
  it('summarises the scope', () => {
    expect(isScoped(scoped)).toBe(true);
    expect(isScoped(everywhere)).toBe(false);
    expect(hasNoServices(unassigned)).toBe(true);
    expect(hasNoServices(scoped)).toBe(false);
    expect(hasNoServices(admin)).toBe(false);

    expect(scopeLabel(everywhere)).toBe('all services');
    expect(scopeLabel(scoped)).toBe('1 service');
    expect(scopeLabel({ all_services: false, services: ['a', 'b'] })).toBe('2 services');
    expect(scopeLabel(unassigned)).toBe('no services');
    expect(accessLabel(scoped)).toBe('viewer · 1 service');
    expect(accessLabel(admin)).toBe('admin · all services');
  });

  it('describes a scope for the Users table', () => {
    expect(describeScope({ all_services: true, services: [] })).toBe('All services');
    expect(describeScope({ all_services: false, services: [] })).toBe('No services');
    expect(describeScope({ all_services: false, services: ['a', 'b'] })).toBe('a, b');
    expect(describeScope({ all_services: false, services: ['a', 'b', 'c', 'd'] })).toBe('a, b +2 more');
  });
});
