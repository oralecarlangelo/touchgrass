import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import AccessDenied from './AccessDenied.tsx';
import NoServicesAssigned from './NoServicesAssigned.tsx';

describe('AccessDenied', () => {
  it('names what is missing and offers a way back', () => {
    const html = renderToStaticMarkup(
      <AccessDenied
        what="the service “docs-site”"
        detail="Your account is limited to 1 service."
        backLabel="Back to Dashboard"
        onBack={() => undefined}
      />,
    );

    expect(html).toContain('role="alert"');
    expect(html).toContain('have access to the service “docs-site”');
    expect(html).toContain('Your account is limited to 1 service.');
    expect(html).toContain('Back to Dashboard');
  });
});

describe('NoServicesAssigned', () => {
  it('tells a new scoped account who to ask', () => {
    const html = renderToStaticMarkup(<NoServicesAssigned username="dev" onRetry={() => undefined} />);

    expect(html).toContain('No services assigned yet');
    expect(html).toContain('Ask an admin');
    expect(html).toContain('Check again');
  });
});
