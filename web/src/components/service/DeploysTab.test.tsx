import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import DeploysTab from './DeploysTab.tsx';
import type { ServiceView } from '@/lib/api.ts';

const recreate = { id: 'billing-worker', name: 'Billing worker', strategy: 'recreate' } as ServiceView;

function render(props: { canTrigger: boolean; canRecord: boolean; recordAs: string | null }): string {
  return renderToStaticMarkup(
    <DeploysTab
      service={recreate}
      onServiceChanged={() => undefined}
      onUnauthorized={() => undefined}
      {...props}
    />,
  );
}

describe('DeploysTab', () => {
  it('shows a viewer the history and no controls', () => {
    const html = render({ canTrigger: false, canRecord: false, recordAs: 'dana' });

    expect(html).toContain('Downtime per deploy');
    expect(html).not.toContain('Record deploy');
    expect(html).not.toContain('Start rollback');
    expect(html).not.toContain('Rollback re-runs the previous image');
  });

  it('shows a developer the deploy controls and records under their own name', () => {
    const html = render({ canTrigger: true, canRecord: true, recordAs: 'sam' });

    expect(html).toContain('Start rollback');
    expect(html).toContain('Record deploy');
    expect(html).toContain('Recorded as sam');
    expect(html).not.toContain('id="actor-billing-worker"');
  });

  it('lets an admin name the actor of a manual record', () => {
    const html = render({ canTrigger: true, canRecord: true, recordAs: null });

    expect(html).toContain('id="actor-billing-worker"');
    expect(html).not.toContain('Recorded as');
  });
});
