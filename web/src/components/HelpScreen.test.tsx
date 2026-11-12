import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import HelpScreen, { filterHelpFaqs } from './HelpScreen.tsx';
import { makeCan, type Capability } from '@/lib/access.ts';

function canWith(capabilities: Capability[]) {
  return makeCan({
    authenticated: true,
    username: 'someone',
    role: 'viewer',
    all_services: false,
    services: ['billing-worker'],
    capabilities,
  });
}

const viewerCan = canWith(['service.read', 'logs.read', 'alerts.read']);
const developerCan = canWith([
  'service.read',
  'logs.read',
  'alerts.read',
  'deploy.trigger',
  'deploy.record',
  'alerts.manage',
  'notifications.ack',
]);
const adminCan = canWith([
  'service.read',
  'deploy.trigger',
  'alerts.manage',
  'databases.backup',
  'services.manage',
]);

describe('HelpScreen', () => {
  it('searches FAQ questions, answers, and categories case-insensitively', () => {
    expect(filterHelpFaqs('STALE').map((faq) => faq.question)).toContain(
      'Why does Touchgrass say collector coverage is stale or partial?',
    );
    expect(filterHelpFaqs('containment').map((faq) => faq.question)).toContain(
      'Does Touchgrass automatically stop a suspicious container?',
    );
    expect(filterHelpFaqs('access').map((faq) => faq.question)).toContain(
      'What can a read-only user do?',
    );
  });

  it('shows each task only to users who can do it', () => {
    const viewer = renderToStaticMarkup(<HelpScreen can={viewerCan} onView={() => undefined} />);
    const developer = renderToStaticMarkup(<HelpScreen can={developerCan} onView={() => undefined} />);
    const admin = renderToStaticMarkup(<HelpScreen can={adminCan} onView={() => undefined} />);

    expect(viewer).toContain('Ask an admin to change policy or infrastructure.');
    expect(viewer).not.toContain('Configure alert rules');
    expect(viewer).not.toContain('Protect database recovery');

    expect(developer).toContain('You can deploy and roll back your services');
    expect(developer).not.toContain('You have read-only access');
    expect(developer).toContain('Configure alert rules');
    expect(developer).not.toContain('Protect database recovery');

    expect(admin).not.toContain('You have read-only access');
    expect(admin).not.toContain('You can deploy and roll back your services');
    expect(admin).toContain('Configure alert rules');
    expect(admin).toContain('Protect database recovery');
  });

  it('explains the developer role', () => {
    expect(filterHelpFaqs('developer').map((faq) => faq.question)).toContain('What can a developer do?');
  });
});
