import { useCallback, useEffect, useRef, useState } from 'react';

import EmptyState from './components/EmptyState.tsx';
import ErrorState from './components/ErrorState.tsx';
import Dashboard from './components/Dashboard.tsx';
import DatabasesScreen from './components/DatabasesScreen.tsx';
import HistoryScreen from './components/HistoryScreen.tsx';
import ImagesScreen from './components/ImagesScreen.tsx';
import IssuesScreen from './components/IssuesScreen.tsx';
import KeysScreen from './components/KeysScreen.tsx';
import LoginForm from './components/LoginForm.tsx';
import LogsScreen from './components/LogsScreen.tsx';
import NotificationsScreen from './components/NotificationsScreen.tsx';
import RulesScreen from './components/RulesScreen.tsx';
import ServicesList from './components/ServicesList.tsx';
import ServiceWorkspace from './components/service/ServiceWorkspace.tsx';
import SystemScreen from './components/SystemScreen.tsx';
import AppShell from './components/layout/AppShell.tsx';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Toaster } from '@/components/ui/sonner';
import {
  fetchServices,
  fetchSession,
  isUnauthorized,
  login,
  logout,
  type ServiceView,
} from './lib/api.ts';
import type { ViewId } from './lib/views.ts';
import { parseHash, writeHash } from './lib/hash.ts';

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
  const [view, setView] = useState<ViewId>(() => parseHash(window.location.hash).view);

  // Deep-link: the hash mirrors the view + selected service both ways.
  // A linked service resolves once the inventory loads.
  const pendingServiceRef = useRef<string | null>(parseHash(window.location.hash).serviceId);

  useEffect(() => {
    writeHash(view, selected?.id ?? null);
  }, [view, selected]);

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
    setView('dashboard');
    setLoginError('Session expired. Log in again to continue.');
  }, []);

  const load = useCallback(async () => {
    setRefreshing(true);
    setError(null);

    try {
      const fetched = await fetchServices();
      setServices(fetched);

      const pending = pendingServiceRef.current;

      if (pending !== null) {
        pendingServiceRef.current = null;
        setSelected(fetched.find((service) => service.id === pending) ?? null);
      } else {
        setSelected((current) =>
          current === null
            ? null
            : (fetched.find((service) => service.id === current.id) ?? current),
        );
      }
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
    setView('dashboard');
    setPassword('');
  }, []);

  const handleView = useCallback((next: ViewId) => {
    if (next !== 'services') {
      setSelected(null);
    }

    setView(next);
  }, []);

  const handleSelectService = useCallback(
    (id: string | null) => {
      setSelected(id === null ? null : (services?.find((service) => service.id === id) ?? null));
      setView('services');
    },
    [services],
  );

  if (session === null) {
    return (
      <div className="bg-background flex min-h-screen items-center justify-center px-4">
        {sessionError !== null ? (
          <div className="w-full max-w-sm">
            <ErrorState
              title="Couldn't check the session"
              message={sessionError}
              onRetry={() => void refreshSession()}
            />
          </div>
        ) : (
          <div className="w-full max-w-sm space-y-2" aria-label="Checking the admin session">
            <Skeleton className="h-8 w-40" />
            <Skeleton className="h-4 w-full" />
          </div>
        )}
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
    <>
      <AppShell
        view={view}
        onView={handleView}
        services={services ?? []}
        selectedId={selected?.id ?? null}
        onSelectService={handleSelectService}
        onLogout={() => void handleLogout()}
        onUnauthorized={handleUnauthorized}
      >
        {selected !== null ? (
          <ServiceWorkspace
            service={selected}
            onBack={() => setSelected(null)}
            onServiceChanged={() => void load()}
            onUnauthorized={handleUnauthorized}
          />
        ) : view === 'dashboard' ? (
          <Dashboard
            services={services}
            onSelectService={handleSelectService}
            onView={handleView}
            onUnauthorized={handleUnauthorized}
          />
        ) : view === 'audit' ? (
          <HistoryScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'notifications' ? (
          <NotificationsScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'issues' ? (
          <IssuesScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'logs' ? (
          <LogsScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'images' ? (
          <ImagesScreen onUnauthorized={handleUnauthorized} />
        ) : view === 'system' ? (
          <SystemScreen onUnauthorized={handleUnauthorized} />
        ) : view === 'databases' ? (
          <DatabasesScreen onUnauthorized={handleUnauthorized} />
        ) : view === 'rules' ? (
          <RulesScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : view === 'keys' ? (
          <KeysScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
        ) : (
          <>
            <div className="flex items-center justify-between">
              <h1 className="text-lg font-semibold tracking-tight">Services</h1>
              <Button
                variant="outline"
                size="sm"
                onClick={() => void load()}
                disabled={refreshing}
              >
                {refreshing ? 'Checking…' : 'Refresh'}
              </Button>
            </div>

            {error !== null && (
              <ErrorState
                title="Couldn't load services"
                message={error}
                onRetry={() => void load()}
              />
            )}

            {error === null && services !== null && services.length === 0 && (
              <EmptyState
                title="No services on the radar"
                body="Grass: touched. Add services to the inventory to watch them here."
              />
            )}

            {error === null && (services === null || services.length > 0) && (
              <ServicesList services={services} onSelect={handleSelectService} />
            )}
          </>
        )}
      </AppShell>
      <Toaster />
    </>
  );
}
