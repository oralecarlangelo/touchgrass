import type { FormEvent } from 'react';

export default function LoginForm({
  password,
  onPasswordChange,
  onSubmit,
  submitting,
  error,
}: {
  password: string;
  onPasswordChange: (value: string) => void;
  onSubmit: () => void;
  submitting: boolean;
  error: string | null;
}) {
  function handleSubmit(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault();
    onSubmit();
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <main className="mx-auto max-w-md px-4 py-16">
        <h1 className="text-2xl font-bold text-gray-900">touchgrass</h1>
        <p className="mt-1 text-sm text-gray-500">Admin login. No vibe check without it.</p>

        <form
          onSubmit={handleSubmit}
          aria-label="Admin login"
          className="mt-6 space-y-3 rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
        >
          <label className="block text-sm text-gray-700">
            Admin password
            <input
              type="password"
              value={password}
              onChange={(event) => onPasswordChange(event.target.value)}
              autoComplete="current-password"
              aria-label="Admin password"
              className="mt-1 w-full rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-sm text-gray-900"
            />
          </label>

          {error !== null && (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-3">
              <p className="text-sm text-red-700">{error}</p>
            </div>
          )}

          <button
            type="submit"
            disabled={submitting || password === ''}
            className="w-full rounded-md border border-gray-300 bg-white px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            {submitting ? 'Checking…' : 'Log in'}
          </button>
        </form>
      </main>
    </div>
  );
}
