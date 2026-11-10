// The managed-account password policy, mirrored from the server
// (internal/service/users.go) so the form can explain a refusal before
// the request. The server stays the authority.
export const minPasswordLength = 12;
export const maxPasswordBytes = 72;

// Returns why a new password cannot be submitted yet, or null when it
// can. Empty fields return null: an untouched form shows no error, the
// submit button is simply disabled (see canSubmitPassword).
export function newPasswordProblem(next: string, confirm: string, current: string): string | null {
  if (next === '') {
    return null;
  }

  if (next.length < minPasswordLength) {
    const missing = minPasswordLength - next.length;

    return `Use at least ${minPasswordLength} characters (${missing} more).`;
  }

  if (new TextEncoder().encode(next).length > maxPasswordBytes) {
    return `Use at most ${maxPasswordBytes} bytes.`;
  }

  if (current !== '' && next === current) {
    return 'The new password must differ from the current one.';
  }

  if (confirm !== '' && confirm !== next) {
    return 'The two new passwords do not match.';
  }

  return null;
}

export function canSubmitPassword(next: string, confirm: string, current: string): boolean {
  return (
    current !== '' &&
    next !== '' &&
    confirm === next &&
    newPasswordProblem(next, confirm, current) === null
  );
}
