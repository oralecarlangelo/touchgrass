export type ViewId =
  | 'dashboard'
  | 'services'
  | 'issues'
  | 'logs'
  | 'notifications'
  | 'images'
  | 'system'
  | 'audit'
  | 'rules'
  | 'keys'
  | 'databases';

export interface NavTab {
  id: ViewId;
  label: string;
}

export const primaryTabs: NavTab[] = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'services', label: 'Services' },
  { id: 'issues', label: 'Issues' },
  { id: 'logs', label: 'Logs' },
  { id: 'notifications', label: 'Notifications' },
  { id: 'images', label: 'Images' },
  { id: 'system', label: 'System' },
  { id: 'databases', label: 'Databases' },
  { id: 'audit', label: 'Audit' },
];

export const moreTabs: NavTab[] = [
  { id: 'rules', label: 'Rules' },
  { id: 'keys', label: 'API keys' },
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
