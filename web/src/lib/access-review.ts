import type { AccessMatrix, AccessMatrixRow, ManagedUser } from './api.ts';

// One account that can reach a service, and how it got there.
export interface Reach {
  username: string;
  role: string;
  // True when the account has every service, so this one came with the
  // scope and was not granted by name.
  viaAllServices: boolean;
  // A disabled account keeps its access on paper but cannot sign in.
  disabled: boolean;
}

export interface ServiceReach {
  serviceId: string;
  serviceName: string;
  accounts: Reach[];
}

// Roles from the most to the least powerful, the order people are
// listed in: who can change the service comes first.
const rolePower = ['admin', 'developer', 'viewer'];

function byPowerThenName(a: Reach, b: Reach): number {
  const power = rolePower.indexOf(a.role) - rolePower.indexOf(b.role);

  return power !== 0 ? power : a.username.localeCompare(b.username);
}

function reachOf(user: ManagedUser): Reach {
  return {
    username: user.username,
    role: user.role,
    viaAllServices: user.all_services,
    disabled: user.disabled,
  };
}

// serviceReach answers "who can reach this service, and as what" for
// every service: accounts with every service, then accounts it was
// granted to by name.
export function serviceReach(
  services: { id: string; name: string }[],
  users: ManagedUser[],
): ServiceReach[] {
  return services.map((service) => ({
    serviceId: service.id,
    serviceName: service.name,
    accounts: users
      .filter((user) => user.all_services || user.services.includes(service.id))
      .map(reachOf)
      .sort(byPowerThenName),
  }));
}

// hostReach lists the accounts with every service: the only ones that
// see the host screens (Fleet, Security, Databases, Images, API keys)
// and the audit rows that belong to no service.
export function hostReach(users: ManagedUser[]): Reach[] {
  return users
    .filter((user) => user.all_services)
    .map(reachOf)
    .sort(byPowerThenName);
}

// unassigned lists the accounts that can sign in and see nothing.
export function unassigned(users: ManagedUser[]): Reach[] {
  return users
    .filter((user) => !user.all_services && user.services.length === 0)
    .map(reachOf)
    .sort(byPowerThenName);
}

export interface MatrixSection {
  scope: string;
  title: string;
  explanation: string;
  rows: AccessMatrixRow[];
}

const scopeText: Record<string, { title: string; explanation: string }> = {
  self: {
    title: 'Own account',
    explanation: 'Every signed-in user, whatever their services.',
  },
  service: {
    title: 'Per service',
    explanation:
      'Applies to the services in the user’s scope: every service, or the ones selected for them.',
  },
  fleet: {
    title: 'Whole host',
    explanation:
      'Only for a user with all services. A user limited to selected services is refused these whatever their role.',
  },
};

// matrixSections groups the matrix by where a capability applies,
// keeping the server's order inside each group.
export function matrixSections(matrix: AccessMatrix): MatrixSection[] {
  const sections: MatrixSection[] = [];

  for (const row of matrix.capabilities) {
    let section = sections.find((entry) => entry.scope === row.scope);

    if (section === undefined) {
      const text = scopeText[row.scope] ?? { title: row.scope, explanation: '' };
      section = { scope: row.scope, title: text.title, explanation: text.explanation, rows: [] };
      sections.push(section);
    }

    section.rows.push(row);
  }

  return sections;
}

// grantOf reads one cell: held, held with a stated limit, or not held.
export function grantOf(row: AccessMatrixRow, role: string): 'yes' | 'limited' | 'no' {
  if (row.roles[role] !== true) {
    return 'no';
  }

  return (row.limits[role] ?? '') !== '' ? 'limited' : 'yes';
}
