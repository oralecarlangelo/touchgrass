import { describe, expect, it } from 'vitest';

import { canSubmitPassword, newPasswordProblem } from './password.ts';

describe('newPasswordProblem', () => {
  it('stays quiet on an untouched form', () => {
    expect(newPasswordProblem('', '', '')).toBeNull();
    expect(canSubmitPassword('', '', 'current-password')).toBe(false);
  });

  it('counts the characters still missing', () => {
    expect(newPasswordProblem('short', '', 'x')).toBe('Use at least 12 characters (7 more).');
  });

  it('refuses a password over the 72-byte bcrypt limit, counted in bytes', () => {
    expect(newPasswordProblem('x'.repeat(73), '', 'c')).toBe('Use at most 72 bytes.');
    // 25 three-byte characters are 75 bytes but only 25 characters.
    expect(newPasswordProblem('€'.repeat(25), '', 'c')).toBe('Use at most 72 bytes.');
  });

  it('refuses reusing the current password', () => {
    expect(newPasswordProblem('same-password-1', '', 'same-password-1')).toBe(
      'The new password must differ from the current one.'
    );
  });

  it('reports a mismatch only once the repeat field has input', () => {
    expect(newPasswordProblem('a-fine-password', '', 'current')).toBeNull();
    expect(newPasswordProblem('a-fine-password', 'a-fine-passwor', 'current')).toBe(
      'The two new passwords do not match.'
    );
  });
});

describe('canSubmitPassword', () => {
  it('needs the current password, a valid new one and a matching repeat', () => {
    expect(canSubmitPassword('a-fine-password', 'a-fine-password', 'current')).toBe(true);
    expect(canSubmitPassword('a-fine-password', 'a-fine-password', '')).toBe(false);
    expect(canSubmitPassword('a-fine-password', '', 'current')).toBe(false);
    expect(canSubmitPassword('short', 'short', 'current')).toBe(false);
  });
});
