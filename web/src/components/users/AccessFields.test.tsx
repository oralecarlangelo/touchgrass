import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import AccessFields, { accessProblem, defaultAccess, normalizeAccess } from './AccessFields.tsx';
import type { ServiceView } from '@/lib/api.ts';

const services = [
  { id: 'billing-worker', name: 'Billing worker' },
  { id: 'docs-site', name: 'docs-site' },
] as ServiceView[];

describe('normalizeAccess', () => {
  it('never sends a scoped admin or a list alongside all services', () => {
    expect(normalizeAccess({ role: 'admin', all_services: false, services: ['docs-site'] })).toEqual({
      role: 'admin',
      all_services: true,
      services: [],
    });
    expect(normalizeAccess({ role: 'viewer', all_services: true, services: ['docs-site'] })).toEqual({
      role: 'viewer',
      all_services: true,
      services: [],
    });
    expect(
      normalizeAccess({ role: 'viewer', all_services: false, services: ['docs-site', 'billing-worker'] }),
    ).toEqual({ role: 'viewer', all_services: false, services: ['billing-worker', 'docs-site'] });
  });

  it('starts a new user as an all-services viewer', () => {
    expect(defaultAccess()).toEqual({ role: 'viewer', all_services: true, services: [] });
  });
});

describe('accessProblem', () => {
  it('asks for at least one service when the scope is a list', () => {
    expect(accessProblem({ role: 'viewer', all_services: false, services: [] })).toContain('at least one');
    expect(accessProblem({ role: 'viewer', all_services: false, services: ['docs-site'] })).toBeNull();
    expect(accessProblem({ role: 'admin', all_services: false, services: [] })).toBeNull();
    expect(accessProblem(defaultAccess())).toBeNull();
  });
});

describe('AccessFields', () => {
  it('shows the checklist only for a selected-services scope', () => {
    const scoped = renderToStaticMarkup(
      <AccessFields
        idPrefix="t"
        access={{ role: 'viewer', all_services: false, services: ['billing-worker'] }}
        onChange={() => undefined}
        services={services}
      />,
    );
    const everywhere = renderToStaticMarkup(
      <AccessFields idPrefix="t" access={defaultAccess()} onChange={() => undefined} services={services} />,
    );

    expect(scoped).toContain('Billing worker');
    expect(scoped).toContain('docs-site');
    expect(scoped).toContain('Services this user can see');
    expect(everywhere).not.toContain('Services this user can see');
    expect(everywhere).toContain('host screens');
  });

  it('locks an admin to every service', () => {
    const html = renderToStaticMarkup(
      <AccessFields
        idPrefix="t"
        access={{ role: 'admin', all_services: false, services: ['docs-site'] }}
        onChange={() => undefined}
        services={services}
      />,
    );

    expect(html).toContain('An admin always has every service');
    expect(html).not.toContain('Selected services');
    expect(html).not.toContain('Services this user can see');
  });
});
