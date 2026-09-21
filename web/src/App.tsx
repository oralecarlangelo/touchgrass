import { useCallback, useEffect, useState } from 'react';
import AuditLog from './components/AuditLog.tsx';
import Issues from './components/Issues.tsx';
import LoginForm from './components/LoginForm.tsx';
import Logs from './components/Logs.tsx';
import NotificationCenter from './components/NotificationCenter.tsx';
import ServiceCard from './components/ServiceCard.tsx';
import ServiceDetail from './components/ServiceDetail.tsx';
import {
  fetchServices,
  fetchSession,
  isUnauthorized,
  login,
  logout,
  type ServiceView,
} from './lib/api.ts';

const refreshIntervalMs = 30_000;

export default function App() {
  const [session, setSession] = useState<boolean | null>(null);
  const [sessionError, setSessionError] = useState<string | null>(null);
  const [password, setPassword] = useState('');
  const [loginError, setLoginError] = useState<string | null>(null);
  const [loggingIn, setLoggingIn] = useState(false);
  const [services, setServices] = useState<ServiceView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [selected, setSelected] = useState<ServiceView | null>(null);
  const [view, setView] = useState<'services' | 'audit' | 'notifications' | 'issues' | 'logs'>(
    'services',
  );

  const refreshSession = useCallback(async () => {
    setSessionError(null);

    try {
      setSession(await fetchSession());
    } catch (err) {
      setSessionError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refreshSession();
  }, [refreshSession]);

  const handleUnauthorized = useCallback(() => {
    setSession(false);
    setServices(null);
    setSelected(null);
    setView('services');
    setLoginError('Session expired. Log in again to continue.');
  }, []);

  const load = useCallback(async () => {
    setRefreshing(true);
    setError(null);

    try {
      const fetched = await fetchServices();
      setServices(fetched);
      setSelected((current) =>
        current === null ? null : (fetched.find((service) => service.id === current.id) ?? current),
      );
    } catch (err) {
      if (isUnauthorized(err)) {
        handleUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRefreshing(false);
    }
  }, [handleUnauthorized]);

  useEffect(() => {
    if (!session) {
      return;
    }

    void load();
    const timer = setInterval(() => void load(), refreshIntervalMs);

    return () => clearInterval(timer);
  }, [session, load]);

  const handleLogin = useCallback(async () => {
    setLoggingIn(true);
    setLoginError(null);

    try {
      await login(password);
      setPassword('');
      setSession(true);
    } catch (err) {
      setLoginError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoggingIn(false);
    }
  }, [password]);

  const handleLogout = useCallback(async () => {
    setError(null);

    try {
      await logout();
    } catch (err) {
      if (!isUnauthorized(err)) {
        setError(err instanceof Error ? err.message : String(err));
        return;
      }
    }

    setSession(false);
    setServices(null);
    setSelected(null);
    setView('services');
    setPassword('');
  }, []);

  if (session === null) {
    return (
      <div className="min-h-screen bg-gray-50">
        <main className="mx-auto max-w-md px-4 py-16">
          {sessionError !== null ? (
            <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-4">
              <p className="text-sm font-medium text-red-800">Couldn&apos;t check the session</p>
              <p className="mt-1 text-sm text-red-700">{sessionError}</p>
              <button
                type="button"
                onClick={() => void refreshSession()}
                className="mt-2 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-800 hover:bg-red-100"
              >
                Retry
              </button>
            </div>
          ) : (
            <p className="text-sm text-gray-500">Checking the admin session…</p>
          )}
        </main>
      </div>
    );
  }

  if (!session) {
    return (
      <LoginForm
        password={password}
        onPasswordChange={setPassword}
        onSubmit={() => void handleLogin()}
        submitting={loggingIn}
        error={loginError}
      />
    );
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <header className="border-b border-gray-200 bg-white">
        <div className="mx-auto flex max-w-5xl items-center gap-3 px-4 py-4">
          <h1 className="text-xl font-bold text-gray-900">touchgrass</h1>
          <p className="text-sm text-gray-500">go touch grass — prod is covered.</p>
          <button
            type="button"
            onClick={() => setView('services')}
            aria-label="Show services"
            aria-current={view === 'services' ? 'page' : undefined}
            className="ml-auto rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
          >
            Services
          </button>
          <button
            type="button"
            onClick={() => {
              setSelected(null);
              setView('audit');
            }}
            aria-label="Show audit log"
            aria-current={view === 'audit' ? 'page' : undefined}
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
          >
            Audit
          </button>
          <button
            type="button"
            onClick={() => {
              setSelected(null);
              setView('notifications');
            }}
            aria-label="Show notifications"
            aria-current={view === 'notifications' ? 'page' : undefined}
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
          >
            Notifications
          </button>
          <button
            type="button"
            onClick={() => {
              setSelected(null);
              setView('issues');
            }}
            aria-label="Show issues"
            aria-current={view === 'issues' ? 'page' : undefined}
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
          >
            Issues
          </button>
          <button
            type="button"
            onClick={() => {
              setSelected(null);
              setView('logs');
            }}
            aria-label="Show logs"
            aria-current={view === 'logs' ? 'page' : undefined}
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
          >
            Logs
          </button>
          <button
            type="button"
            onClick={() => void load()}
            disabled={refreshing || view !== 'services'}
            aria-label="Refresh services"
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            {refreshing ? 'Checking…' : 'Refresh'}
          </button>
          <button
            type="button"
            onClick={() => void handleLogout()}
            aria-label="Log out"
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
          >
            Log out
          </button>
        </div>
      </header>

      <main className="mx-auto max-w-5xl space-y-4 px-4 py-6">
        {selected !== null ? (
          <ServiceDetail
            service={selected}
            onBack={() => setSelected(null)}
            onServiceChanged={() => void load()}
          />
        ) : view === 'audit' ? (
          <AuditLog services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'notifications' ? (
          <NotificationCenter services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'issues' ? (
          <Issues services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'logs' ? (
          <Logs services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : (
          <>
            {error !== null && (
              <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-4">
                <p className="text-sm font-medium text-red-800">Couldn&apos;t load services</p>
                <p className="mt-1 text-sm text-red-700">{error}</p>
                <button
                  type="button"
                  onClick={() => void load()}
                  className="mt-2 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-800 hover:bg-red-100"
                >
                  Retry
                </button>
              </div>
            )}

            {error === null && services === null && (
              <p className="text-sm text-gray-500">Vibe-checking your services…</p>
            )}

            {error === null && services !== null && services.length === 0 && (
              <p className="text-sm text-gray-500">No services on the radar. Grass: touched. 🌱</p>
            )}

            {services !== null &&
              services.map((service) => (
                <ServiceCard key={service.id} service={service} onSelect={setSelected} />
              ))}
          </>
        )}
      </main>
    </div>
  );
}
