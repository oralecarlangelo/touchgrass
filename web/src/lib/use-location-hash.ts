import { useSyncExternalStore } from 'react';

import { hashUpdatedEvent, parseHash } from './hash.ts';
import type { ViewId } from './views.ts';

function subscribe(listener: () => void) {
  window.addEventListener('hashchange', listener);
  window.addEventListener(hashUpdatedEvent, listener);
  return () => {
    window.removeEventListener('hashchange', listener);
    window.removeEventListener(hashUpdatedEvent, listener);
  };
}

export function useLocationHash(view: ViewId): string | null {
  const hash = useSyncExternalStore(subscribe, () => window.location.hash);
  return parseHash(hash).view === view ? hash : null;
}
