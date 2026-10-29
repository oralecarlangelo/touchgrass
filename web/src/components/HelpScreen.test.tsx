import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import HelpScreen, { filterHelpFaqs } from './HelpScreen.tsx';

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

  it('shows admin actions only to admins', () => {
    const viewer = renderToStaticMarkup(
      <HelpScreen isAdmin={false} onView={() => undefined} />,
    );
    const admin = renderToStaticMarkup(
      <HelpScreen isAdmin onView={() => undefined} />,
    );

    expect(viewer).toContain('Ask an admin to change policy or infrastructure.');
    expect(viewer).not.toContain('Configure alert rules');
    expect(admin).toContain('Configure alert rules');
  });
});
