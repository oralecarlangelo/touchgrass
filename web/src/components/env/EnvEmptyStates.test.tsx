import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { EnvNotConfigured } from './EnvEmptyStates.tsx';

describe('EnvNotConfigured', () => {
  it('tells an admin to create a key file and point the variable at its path', () => {
    const html = renderToStaticMarkup(<EnvNotConfigured reason="key_missing" serviceId="billing-worker" isAdmin />);
    expect(html).toContain('head -c 32 /dev/urandom');
    expect(html).toContain('chmod 0600 /etc/touchgrass/env.key');
    expect(html).toContain('export TOUCHGRASS_ENV_KEY_FILE=/etc/touchgrass/env.key');
    // The variable is a path, and the key is raw bytes: never a base64 string assigned inline.
    expect(html).not.toContain('openssl');
    expect(html).not.toContain('TOUCHGRASS_ENV_KEY_FILE=$(');
  });

  it('shows no host commands to a non-admin', () => {
    const html = renderToStaticMarkup(
      <EnvNotConfigured reason="key_missing" serviceId="billing-worker" isAdmin={false} />,
    );
    expect(html).toContain('Ask an admin');
    expect(html).not.toContain('/dev/urandom');
  });
});
