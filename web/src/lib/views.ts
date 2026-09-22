export type ViewId =
  | 'dashboard'
  | 'services'
  | 'issues'
  | 'logs'
  | 'notifications'
  | 'images'
  | 'fleet'
  | 'audit'
  | 'rules'
  | 'keys'
  | 'databases';

export interface NavTab {
  id: ViewId;
  label: string;
}

export interface NavGroup {
  id: string;
  label: string;
  items: NavTab[];
}

// Groups follow the operator workflow: observe the fleet, respond to
// signals, operate infrastructure, administer configuration. New views
// slot into the group matching their job — never a fifth top-level
// bucket without retiring one.
export const navGroups: NavGroup[] = [
  {
    id: 'overview',
    label: 'Overview',
    items: [
      { id: 'dashboard', label: 'Dashboard' },
      { id: 'services', label: 'Services' },
      { id: 'fleet', label: 'Fleet' },
    ],
  },
  {
    id: 'signals',
    label: 'Signals',
    items: [
      { id: 'issues', label: 'Issues' },
      { id: 'logs', label: 'Logs' },
      { id: 'notifications', label: 'Notifications' },
    ],
  },
  {
    id: 'operations',
    label: 'Operations',
    items: [
      { id: 'databases', label: 'Databases' },
      { id: 'images', label: 'Images' },
    ],
  },
  {
    id: 'administration',
    label: 'Administration',
    items: [
      { id: 'rules', label: 'Rules' },
      { id: 'keys', label: 'API keys' },
      { id: 'audit', label: 'Audit' },
    ],
  },
];

export function healthDotClass(health: string): string {
  switch (health) {
    case 'healthy':
      return 'bg-emerald-500';
    case 'unhealthy':
      return 'bg-red-500';
    default:
      return 'bg-muted-foreground/50';
  }
}
