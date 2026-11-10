import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';

import { ApiError } from '@/lib/api';

import FirstLoginPassword from './FirstLoginPassword.tsx';
import { passwordChangeError } from './PasswordFields.tsx';

function render(knownCurrent: string): string {
  return renderToStaticMarkup(
    <FirstLoginPassword
      username="admin"
      knownCurrent={knownCurrent}
      onChanged={() => undefined}
      onLogout={() => undefined}
    />
  );
}

describe('FirstLoginPassword', () => {
  it('does not ask again for the one-time password typed at login', () => {
    const html = render('one-time-password-from-login');

    expect(html).toContain('Set your password');
    expect(html).not.toContain('Current password');
    expect(html).toContain('New password');
    expect(html).toContain('Repeat the new password');
    expect(html).not.toContain('one-time-password-from-login');
  });

  it('asks for the current password after a reload lost it', () => {
    expect(render('')).toContain('Current password');
  });

  it('offers a way out and keeps save disabled until the form is valid', () => {
    const html = render('');

    expect(html).toContain('Log out');
    expect(html).toMatch(/<button[^>]*type="submit"[^>]*disabled/);
  });
});

describe('passwordChangeError', () => {
  it('names the field at fault for a wrong current password', () => {
    const err = new ApiError(
      403,
      'current password is incorrect',
      'invalid_current_password',
      null
    );

    expect(passwordChangeError(err)).toBe('The current password is not right.');
  });

  it('explains the rate limit and passes other messages through', () => {
    expect(passwordChangeError(new ApiError(429, 'too many attempts', 'rate_limited', null))).toBe(
      'Too many attempts. Wait a minute, then try again.'
    );
    expect(
      passwordChangeError(new ApiError(400, 'password is too short', 'invalid_request', null))
    ).toBe('password is too short');
  });
});
