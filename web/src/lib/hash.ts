import type { ViewId } from './views.ts';

const views: ViewId[] = [
  'dashboard',
  'services',
  'issues',
  'logs',
  'notifications',
  'images',
  'fleet',
  'audit',
  'rules',
  'keys',
  'databases',
];

export interface HashState {
  view: ViewId;
  serviceId: string | null;
}

// parseHash reads `#/issues` or `#/services/<id>`; anything else is home.
export function parseHash(hash: string): HashState {
  const parts = hash.replace(/^#\/?/, '').split('/').filter((part) => part !== '');
  const view = views.includes(parts[0] as ViewId) ? (parts[0] as ViewId) : 'dashboard';

  if (view === 'services' && parts[1] !== undefined && parts[1] !== '') {
    return { view, serviceId: decodeURIComponent(parts[1]) };
  }

  return { view, serviceId: null };
}

// writeHash mirrors the state back; replaceState keeps back-button sane.
export function writeHash(view: ViewId, serviceId: string | null): void {
  const next =
    view === 'services' && serviceId !== null
      ? `#/services/${encodeURIComponent(serviceId)}`
      : `#/${view}`;

  if (window.location.hash !== next) {
    window.history.replaceState(null, '', next);
  }
}
