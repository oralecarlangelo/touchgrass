import type { SessionInfo } from './api.ts';
import type { EnvAccess } from './env.ts';
import type { ViewId } from './views.ts';

// Capability names mirror internal/authz. The server decides what a
// user may do; the UI only reads the list it sends in /api/auth/me so
// it can hide what would be refused.
export type Capability =
  | 'session.self'
  | 'service.read'
  | 'metrics.read'
  | 'deploy.read'
  | 'deploy.trigger'
  | 'deploy.record'
  | 'logs.read'
  | 'issues.read'
  | 'alerts.read'
  | 'alerts.manage'
  | 'notifications.ack'
  | 'env.read_keys'
  | 'env.stage'
  | 'env.discard'
  | 'env.import'
  | 'env.apply'
  | 'env.reveal'
  | 'keys.read'
  | 'keys.manage'
  | 'audit.read'
  | 'audit.read_all'
  | 'host.read'
  | 'host.manage'
  | 'security.read'
  | 'security.manage'
  | 'databases.read'
  | 'databases.backup'
  | 'databases.restore'
  | 'services.manage'
  | 'users.manage';

// can(capability) asks "may this user ever do this"; can(capability,
// serviceId) asks "may they do it on this service".
export type Can = (capability: Capability, serviceId?: string) => boolean;

export function makeCan(session: SessionInfo | null): Can {
  const signedIn = session !== null && session.authenticated;
  const held = new Set<string>(signedIn ? (session.capabilities ?? []) : []);
  const allServices = signedIn && session.all_services === true;
  const services = new Set<string>(signedIn ? (session.services ?? []) : []);

  return (capability, serviceId) => {
    if (!held.has(capability)) {
      return false;
    }

    if (serviceId === undefined) {
      return true;
    }

    return allServices || services.has(serviceId);
  };
}

// envAccessFor reads what the user may do with one service's env.
export function envAccessFor(can: Can, serviceId: string, username: string): EnvAccess {
  return {
    stage: can('env.stage', serviceId),
    apply: can('env.apply', serviceId),
    discard: can('env.discard', serviceId),
    import: can('env.import', serviceId),
    reveal: can('env.reveal', serviceId),
    username,
  };
}

// isScoped reports a signed-in user limited to a list of services.
export function isScoped(session: SessionInfo | null): boolean {
  return session !== null && session.authenticated && session.all_services !== true;
}

// hasNoServices reports a scoped user who has been granted nothing yet.
export function hasNoServices(session: SessionInfo | null): boolean {
  return isScoped(session) && (session?.services ?? []).length === 0;
}

interface ViewRequirement {
  capability: Capability | null;
  // Host-level screens stay with all-services users even when the
  // capability itself is per service.
  fleetOnly?: boolean;
  // The screen is for admins whatever the scope, so a refusal is about
  // the role, not about the services.
  adminOnly?: boolean;
}

const viewRequirements: Record<ViewId, ViewRequirement> = {
  dashboard: { capability: 'service.read' },
  services: { capability: 'service.read' },
  fleet: { capability: 'host.read' },
  performance: { capability: 'metrics.read' },
  issues: { capability: 'issues.read' },
  logs: { capability: 'logs.read' },
  notifications: { capability: 'alerts.read' },
  security: { capability: 'security.read' },
  databases: { capability: 'databases.read' },
  environment: { capability: 'env.read_keys' },
  images: { capability: 'host.read' },
  rules: { capability: 'alerts.read' },
  keys: { capability: 'keys.read', fleetOnly: true },
  users: { capability: 'users.manage', adminOnly: true },
  audit: { capability: 'audit.read' },
  help: { capability: null },
};

export function canOpenView(view: ViewId, session: SessionInfo | null, can: Can): boolean {
  const requirement = viewRequirements[view];

  if (requirement.capability === null) {
    return true;
  }

  if (requirement.fleetOnly === true && isScoped(session)) {
    return false;
  }

  return can(requirement.capability);
}

// needsAdmin reports a screen only an admin can open.
export function needsAdmin(view: ViewId): boolean {
  return viewRequirements[view].adminOnly === true;
}

const landingOrder: ViewId[] = ['dashboard', 'logs', 'services', 'issues', 'help'];

// firstAllowedView is where "take me somewhere I can use" goes.
export function firstAllowedView(session: SessionInfo | null, can: Can): ViewId {
  return landingOrder.find((view) => canOpenView(view, session, can)) ?? 'help';
}

// recordsAsSelf reports a user whose manual deploy records always carry
// their own name: the server ignores an "actor" from anyone but an
// admin, so the form does not offer the field.
export function recordsAsSelf(session: SessionInfo): boolean {
  return session.role !== 'admin';
}

function countServices(count: number): string {
  if (count === 0) {
    return 'no services';
  }

  return count === 1 ? '1 service' : `${count} services`;
}

// scopeLabel renders a scope as "all services" or "2 services".
export function scopeLabel(access: { all_services?: boolean; services?: string[] }): string {
  if (access.all_services === true) {
    return 'all services';
  }

  return countServices((access.services ?? []).length);
}

// accessLabel is the header's one-line summary, e.g. "developer · 2 services".
export function accessLabel(session: SessionInfo): string {
  return `${session.role} · ${scopeLabel(session)}`;
}

const listedServices = 2;

// describeScope lists a scope for the Users table: the first service
// ids, then how many more.
export function describeScope(access: { all_services: boolean; services: string[] }): string {
  if (access.all_services) {
    return 'All services';
  }

  if (access.services.length === 0) {
    return 'No services';
  }

  const shown = access.services.slice(0, listedServices).join(', ');
  const rest = access.services.length - listedServices;

  return rest > 0 ? `${shown} +${rest} more` : shown;
}
