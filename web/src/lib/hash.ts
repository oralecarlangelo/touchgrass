import type { ViewId } from './views.ts';

const views: ViewId[] = [
  'dashboard',
  'services',
  'performance',
  'issues',
  'logs',
  'notifications',
  'security',
  'images',
  'fleet',
  'audit',
  'rules',
  'keys',
  'databases',
  'environment',
  'users',
  'help',
];

export interface HashState {
  view: ViewId;
  serviceId: string | null;
  // Optional workspace tab on a service link: `#/services/<id>/environment`.
  tab: string | null;
}

const tabPattern = /^[a-z]+$/;

// parseHash reads `#/issues`, `#/services/<id>` or `#/services/<id>/<tab>`;
// anything else is home.
export function parseHash(hash: string): HashState {
  const [path] = hash.replace(/^#\/?/, '').split('?', 1);
  const parts = path.split('/').filter((part) => part !== '');
  const view = views.includes(parts[0] as ViewId) ? (parts[0] as ViewId) : 'dashboard';

  if (view === 'services' && parts[1] !== undefined && parts[1] !== '') {
    const tab = parts[2] !== undefined && tabPattern.test(parts[2]) ? parts[2] : null;

    return { view, serviceId: decodeURIComponent(parts[1]), tab };
  }

  return { view, serviceId: null, tab: null };
}

// writeHash mirrors the state back; replaceState keeps back-button sane.
export function writeHash(view: ViewId, serviceId: string | null, tab: string | null = null): void {
  const next =
    view === 'services' && serviceId !== null
      ? `#/services/${encodeURIComponent(serviceId)}${tab !== null && tab !== 'overview' ? `/${tab}` : ''}`
      : `#/${view}`;

  if (window.location.hash !== next) {
    window.history.replaceState(null, '', next);
  }
}
