import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';

import AccessDenied from './components/AccessDenied.tsx';
import EmptyState from './components/EmptyState.tsx';
import ErrorState from './components/ErrorState.tsx';
import ScreenErrorBoundary from './components/ScreenErrorBoundary.tsx';
import Dashboard from './components/Dashboard.tsx';
import FirstLoginPassword from './components/account/FirstLoginPassword.tsx';
import LoginForm from './components/LoginForm.tsx';
import NoServicesAssigned from './components/NoServicesAssigned.tsx';
import ServicesList from './components/ServicesList.tsx';
import type { ServiceTab } from './components/service/ServiceWorkspace.tsx';
import AppShell from './components/layout/AppShell.tsx';
import { Button } from '@/components/ui/button';
import { Skeleton } from '@/components/ui/skeleton';
import { Toaster } from '@/components/ui/sonner';
import {
  fetchServicesSnapshot,
  fetchSession,
  isUnauthorized,
  login,
  logout,
  onForbidden,
  type ServiceView,
  type ServiceFreshness,
  type SessionInfo,
} from './lib/api.ts';
import {
  canOpenView,
  firstAllowedView,
  hasNoServices,
  isScoped,
  makeCan,
  needsAdmin,
  scopeLabel,
} from './lib/access.ts';
import { navGroups, type ViewId } from './lib/views.ts';
import { parseHash, writeHash } from './lib/hash.ts';
import { LatestRequest } from './lib/latest-request.ts';
import { useNotificationPreview } from './lib/use-notification-preview.ts';

const ContainerDetail = lazy(() => import('./components/ContainerDetail.tsx'));
const DatabasesScreen = lazy(() => import('./components/DatabasesScreen.tsx'));
const EnvironmentScreen = lazy(() => import('./components/EnvironmentScreen.tsx'));
const HistoryScreen = lazy(() => import('./components/HistoryScreen.tsx'));
const HelpScreen = lazy(() => import('./components/HelpScreen.tsx'));
const ImagesScreen = lazy(() => import('./components/ImagesScreen.tsx'));
const IssuesScreen = lazy(() => import('./components/IssuesScreen.tsx'));
const KeysScreen = lazy(() => import('./components/KeysScreen.tsx'));
const LogsScreen = lazy(() => import('./components/LogsScreen.tsx'));
const NotificationsScreen = lazy(() => import('./components/NotificationsScreen.tsx'));
const PerformanceScreen = lazy(() => import('./components/PerformanceScreen.tsx'));
const RulesScreen = lazy(() => import('./components/RulesScreen.tsx'));
const SecurityScreen = lazy(() => import('./components/SecurityScreen.tsx'));
const FleetScreen = lazy(() => import('./components/FleetScreen.tsx'));
const UsersScreen = lazy(() => import('./components/UsersScreen.tsx'));
const UpdatesScreen = lazy(() => import('./components/UpdatesScreen.tsx'));
const ServiceWorkspace = lazy(() => import('./components/service/ServiceWorkspace.tsx'));

const refreshIntervalMs = 30_000;

// A refused request re-reads the session, but not more often than this.
const accessRecheckMs = 2_000;

function sameSession(a: SessionInfo | null, b: SessionInfo): boolean {
  return a !== null && JSON.stringify(a) === JSON.stringify(b);
}

function viewLabel(view: ViewId): string {
  const tab = navGroups.flatMap((group) => group.items).find((item) => item.id === view);

  return tab === undefined ? 'this screen' : `the ${tab.label} screen`;
}

export default function App() {
  const [session, setSession] = useState<SessionInfo | null>(null);
  const [sessionError, setSessionError] = useState<string | null>(null);
  const [username, setUsername] = useState('admin');
  const [password, setPassword] = useState('');
  const [loginError, setLoginError] = useState<string | null>(null);
  const [loggingIn, setLoggingIn] = useState(false);
  const [services, setServices] = useState<ServiceView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [servicesUpdatedAt, setServicesUpdatedAt] = useState<string | null>(null);
  const [servicesFreshness, setServicesFreshness] = useState<ServiceFreshness | null>(null);
  const [selected, setSelected] = useState<ServiceView | null>(null);
  const [selectedContainerId, setSelectedContainerId] = useState<string | null>(null);
  const [selectedSecurityFindingId, setSelectedSecurityFindingId] = useState<number | null>(null);
  // A service the user asked for that is outside their scope.
  const [deniedServiceId, setDeniedServiceId] = useState<string | null>(null);
  const [view, setView] = useState<ViewId>(() => parseHash(window.location.hash).view);
  const [serviceTab, setServiceTab] = useState<ServiceTab>(
    () => (parseHash(window.location.hash).tab as ServiceTab | null) ?? 'overview',
  );

  // Deep-link: the hash mirrors the view + selected service both ways.
  // A linked service resolves once the inventory loads.
  const pendingServiceRef = useRef<string | null>(parseHash(window.location.hash).serviceId);
  const servicesRequestRef = useRef(new LatestRequest<Awaited<ReturnType<typeof fetchServicesSnapshot>>>());
  const servicesLoadGenerationRef = useRef(0);

  // The one-time password typed at login, held only until it is replaced.
  const oneTimePasswordRef = useRef('');
  const dataScopeRef = useRef<string | null>(null);

  useEffect(() => {
    writeHash(view, selected?.id ?? null, serviceTab);
  }, [view, selected, serviceTab]);

  const refreshSession = useCallback(async () => {
    setSessionError(null);

    try {
      const next = await fetchSession();

      // Keep the same object when nothing changed, so a re-check does
      // not restart every screen's polling.
      setSession((current) => (sameSession(current, next) ? current : next));
    } catch (err) {
      setSessionError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refreshSession();
  }, [refreshSession]);

  // Access can change while the page is open. Any refused request
  // re-reads the session; the screens below then follow the new access.
  useEffect(() => {
    let lastCheck = 0;

    onForbidden(() => {
      const now = Date.now();

      if (now - lastCheck < accessRecheckMs) {
        return;
      }

      lastCheck = now;
      void refreshSession();
    });

    return () => onForbidden(null);
  }, [refreshSession]);

  const handleUnauthorized = useCallback(() => {
    servicesLoadGenerationRef.current += 1;
    servicesRequestRef.current.cancel();
    setSession({ authenticated: false, username: '', role: '' });
    setServices(null);
    setServicesFreshness(null);
    setServicesUpdatedAt(null);
    setError(null);
    setRefreshing(false);
    setSelected(null);
    setSelectedContainerId(null);
    setSelectedSecurityFindingId(null);
    setDeniedServiceId(null);
    setView('dashboard');
    setLoginError('Session expired. Log in again to continue.');
  }, []);

  const authed = session !== null && session.authenticated;
  const can = useMemo(() => makeCan(session), [session]);
  const scoped = isScoped(session);
  const dataScope = JSON.stringify([
    session?.authenticated,
    session?.username,
    session?.role,
    session?.all_services,
    [...(session?.services ?? [])].sort(),
  ]);
  const canReadNotifications = can('alerts.read') && session?.must_change_password !== true && !hasNoServices(session);
  const notificationPreview = useNotificationPreview(
    dataScope,
    canReadNotifications,
    view === 'dashboard' && selected === null && selectedContainerId === null,
    handleUnauthorized,
  );

  useEffect(() => {
    if (dataScopeRef.current !== null && dataScopeRef.current !== dataScope) {
      servicesLoadGenerationRef.current += 1;
      servicesRequestRef.current.cancel();
      setServices(null);
      setServicesFreshness(null);
      setServicesUpdatedAt(null);
      setError(null);
      setRefreshing(false);
      setSelected(null);
    }
    dataScopeRef.current = dataScope;
  }, [dataScope]);

  const load = useCallback(async () => {
    const generation = servicesLoadGenerationRef.current;
    setRefreshing(true);

    try {
      const snapshot = await servicesRequestRef.current.run(fetchServicesSnapshot);
      if (generation !== servicesLoadGenerationRef.current) return;
      const fetched = snapshot.services;
      setServices(fetched);
      setServicesFreshness(snapshot.freshness);
      setServicesUpdatedAt(snapshot.freshness?.observed_at ?? new Date().toISOString());
      setError(null);

      const pending = pendingServiceRef.current;

      if (pending !== null) {
        pendingServiceRef.current = null;

        const linked = fetched.find((service) => service.id === pending) ?? null;
        setSelected(linked);

        // A scoped user's list is their whole world: a linked service
        // that is not in it is one they may not open.
        if (linked === null && scoped) {
          setDeniedServiceId(pending);
        }
      } else {
        setSelected((current) => {
          if (current === null) {
            return null;
          }

          const fresh = fetched.find((service) => service.id === current.id);

          if (fresh === undefined && scoped) {
            // The open service left the user's scope.
            setDeniedServiceId(current.id);

            return null;
          }

          return fresh ?? current;
        });
      }
    } catch (err) {
      if (generation !== servicesLoadGenerationRef.current) return;
      if (err instanceof DOMException && err.name === 'AbortError') return;
      if (isUnauthorized(err)) {
        handleUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (generation === servicesLoadGenerationRef.current) setRefreshing(false);
    }
  }, [handleUnauthorized, scoped]);

  useEffect(() => {
    // On a temporary password every route but the change answers 403,
    // so nothing is loaded until it has been replaced.
    if (!authed || session === null || session.must_change_password === true) {
      return;
    }

    // Read-only users live in the logs; land them there unless a deep link says otherwise.
    // Anyone who can deploy starts on the dashboard, like an admin.
    if (!can('deploy.trigger') && can('logs.read') && sessionStorage.getItem('tg-landed') === null) {
      sessionStorage.setItem('tg-landed', '1');

      if (window.location.hash === '' || window.location.hash === '#/dashboard') {
        setView('logs');
      }
    }

    void load();
    const refreshVisible = () => { if (!document.hidden) void load(); };
    const timer = setInterval(refreshVisible, refreshIntervalMs);
    document.addEventListener('visibilitychange', refreshVisible);

    return () => {
      clearInterval(timer);
      document.removeEventListener('visibilitychange', refreshVisible);
      servicesRequestRef.current.cancel();
    };
  }, [authed, session, can, load]);

  const handleLogin = useCallback(async () => {
    setLoggingIn(true);
    setLoginError(null);

    try {
      await login(username.trim() === '' ? 'admin' : username.trim(), password);

      const next = await fetchSession();

      // A one-time password is replaced on the next screen; keep what
      // was just typed (memory only) so it is not asked for twice.
      oneTimePasswordRef.current = next.must_change_password === true ? password : '';
      setPassword('');
      setSession(next);
    } catch (err) {
      setLoginError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoggingIn(false);
    }
  }, [username, password]);

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

    servicesLoadGenerationRef.current += 1;
    setSession({ authenticated: false, username: '', role: '' });
    servicesRequestRef.current.cancel();
    setServices(null);
    setServicesFreshness(null);
    setServicesUpdatedAt(null);
    setRefreshing(false);
    setSelected(null);
    setSelectedContainerId(null);
    setSelectedSecurityFindingId(null);
    setDeniedServiceId(null);
    setView('dashboard');
    setPassword('');
    sessionStorage.removeItem('tg-landed');
  }, []);

  const handleView = useCallback((next: ViewId) => {
    if (next !== 'services') {
      pendingServiceRef.current = null;
      setSelected(null);
    }

    setSelectedContainerId(null);
    setDeniedServiceId(null);
    if (next !== 'security') setSelectedSecurityFindingId(null);

    setView(next);
  }, []);

  const handleSelectService = useCallback(
    (id: string | null, tab: ServiceTab = 'overview') => {
      setServiceTab(tab);
      setDeniedServiceId(null);

      if (id === null) {
        setSelected(null);
        setView('services');
        return;
      }

      const found = services?.find((service) => service.id === id) ?? null;

      if (found !== null) {
        setSelected(found);
        setView('services');
        return;
      }

      // Route cache misses through the one inventory coordinator so navigation,
      // polling, and retries cannot issue competing service reads.
      pendingServiceRef.current = id;
      setSelected(null);
      setView('services');
      void load();
    },
    [services, load],
  );

  useEffect(() => {
    const followHash = () => {
      const location = parseHash(window.location.hash);
      if (location.view === 'services' && location.serviceId !== null) {
        handleView('services');
        handleSelectService(location.serviceId, (location.tab ?? 'overview') as ServiceTab);
        return;
      }
      if (location.view === 'services') handleSelectService(null);
      handleView(location.view);
    };
    window.addEventListener('hashchange', followHash);
    return () => window.removeEventListener('hashchange', followHash);
  }, [handleSelectService, handleView]);

  const handleDeletedService = useCallback(
    (id: string) => {
      setSelected((current) => (current?.id === id ? null : current));
      void load();
    },
    [load],
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

  if (!authed || session === null) {
    return (
      <LoginForm
        username={username}
        onUsernameChange={setUsername}
        password={password}
        onPasswordChange={setPassword}
        onSubmit={() => void handleLogin()}
        submitting={loggingIn}
        error={loginError}
      />
    );
  }

  if (session.must_change_password === true) {
    return (
      <FirstLoginPassword
        username={session.username}
        knownCurrent={oneTimePasswordRef.current}
        onChanged={() => {
          oneTimePasswordRef.current = '';
          void refreshSession();
        }}
        onLogout={() => void handleLogout()}
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
        session={session}
        can={can}
        notificationPreview={canReadNotifications ? notificationPreview : undefined}
      >
        <ScreenErrorBoundary key={selectedContainerId ?? selected?.id ?? view}>
          <Suspense fallback={<p className="text-muted-foreground py-6 text-sm" role="status">Loading screen…</p>}>
            {hasNoServices(session) && view !== 'help' ? (
              <NoServicesAssigned username={session.username} onRetry={() => void refreshSession()} />
            ) : deniedServiceId !== null ? (
              <AccessDenied
                what={`the service “${deniedServiceId}”`}
                detail={`Your account is limited to ${scopeLabel(session)}. Ask an admin if you need this one too.`}
                backLabel="Back to your services"
                onBack={() => handleSelectService(null)}
              />
            ) : !canOpenView(view, session, can) ? (
              <AccessDenied
                what={viewLabel(view)}
                detail={
                  scoped && !needsAdmin(view)
                    ? `It covers the whole host, and your account is limited to ${scopeLabel(session)}.`
                    : `Your role (${session.role}) does not include it. Ask an admin if you need it.`
                }
                backLabel="Go to a screen you can use"
                onBack={() => handleView(firstAllowedView(session, can))}
              />
            ) : selectedContainerId !== null ? (
              <ContainerDetail
                id={selectedContainerId}
                onBack={() => setSelectedContainerId(null)}
                onUnauthorized={handleUnauthorized}
                onSelectSecurityFinding={(id) => {
                  setSelectedContainerId(null);
                  setSelectedSecurityFindingId(id);
                  setView('security');
                }}
              />
            ) : selected !== null ? (
              <ServiceWorkspace
                service={selected}
                onBack={() => {
                  setSelected(null);
                  setServiceTab('overview');
                }}
                onServiceChanged={() => void load()}
                onUnauthorized={handleUnauthorized}
                can={can}
                session={session}
                initialTab={serviceTab}
                onTabChange={setServiceTab}
              />
            ) : view === 'dashboard' ? (
              <Dashboard
                services={services}
                servicesError={error}
                servicesRefreshing={refreshing}
                servicesUpdatedAt={servicesUpdatedAt}
                servicesFreshness={servicesFreshness}
                onRetryServices={() => void load()}
                onSelectService={handleSelectService}
                onView={handleView}
                onUnauthorized={handleUnauthorized}
                canHost={can('host.read')}
                notificationPreview={canReadNotifications ? notificationPreview : undefined}
              />
            ) : view === 'audit' ? (
              <HistoryScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
            ) : view === 'help' ? (
              <HelpScreen can={can} onView={handleView} />
            ) : view === 'notifications' ? (
              <NotificationsScreen
                services={services ?? []}
                onUnauthorized={handleUnauthorized}
                isAdmin={can('notifications.ack')}
                onReadChange={notificationPreview.retry}
                onSelectSecurityFinding={(id) => {
                  setSelectedSecurityFindingId(id);
                  setView('security');
                }}
              />
            ) : view === 'security' ? (
              <SecurityScreen
                isAdmin={can('security.manage')}
                onUnauthorized={handleUnauthorized}
                onSelectContainer={setSelectedContainerId}
                initialFindingId={selectedSecurityFindingId}
              />
            ) : view === 'performance' ? (
              <PerformanceScreen
                services={services ?? []}
                onUnauthorized={handleUnauthorized}
                canFleet={can('host.read')}
              />
            ) : view === 'issues' ? (
              <IssuesScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
            ) : view === 'logs' ? (
              <LogsScreen services={services ?? []} onUnauthorized={handleUnauthorized} />
            ) : view === 'images' ? (
              <ImagesScreen onUnauthorized={handleUnauthorized} isAdmin={can('host.manage')} />
            ) : view === 'fleet' ? (
              <FleetScreen
                onUnauthorized={handleUnauthorized}
                onSelectService={handleSelectService}
                onSelectContainer={setSelectedContainerId}
                isAdmin={can('services.manage')}
              />
            ) : view === 'databases' ? (
              <DatabasesScreen onUnauthorized={handleUnauthorized} isAdmin={can('databases.backup')} />
            ) : view === 'environment' ? (
              <EnvironmentScreen
                services={services}
                isAdmin={can('env.apply')}
                onUnauthorized={handleUnauthorized}
                onOpenService={(id) => handleSelectService(id, 'environment')}
              />
            ) : view === 'rules' ? (
              <RulesScreen
                services={services ?? []}
                onUnauthorized={handleUnauthorized}
                isAdmin={can('alerts.manage')}
              />
            ) : view === 'keys' ? (
              <KeysScreen
                services={services ?? []}
                onUnauthorized={handleUnauthorized}
                isAdmin={can('keys.manage')}
              />
            ) : view === 'users' ? (
              <UsersScreen
                services={services ?? []}
                currentUsername={session.username}
                onUnauthorized={handleUnauthorized}
              />
            ) : view === 'updates' ? (
              <UpdatesScreen onUnauthorized={handleUnauthorized} />
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

                {error !== null && services === null && (
                  <ErrorState
                    title="Couldn't load services"
                    message={error}
                    onRetry={() => void load()}
                  />
                )}

                {services !== null && services.length === 0 && (
                  <EmptyState
                    title="No services on the radar"
                    body={
                      can('services.manage')
                        ? 'Nothing is managed yet. Open Fleet, pick a running container and promote it to a managed service to watch and deploy it here.'
                        : 'Nothing is managed yet. An admin adds services from Fleet; they show up here once added.'
                    }
                    action={
                      can('services.manage') ? (
                        <Button size="sm" onClick={() => handleView('fleet')}>
                          Open Fleet
                        </Button>
                      ) : undefined
                    }
                  />
                )}

                {error !== null && services !== null && services.length > 0 && (
                  <div role="alert" className="border-destructive/40 flex items-center justify-between rounded-lg border p-3 text-sm">
                    <span>Refresh failed. Showing last known services.</span>
                    <Button variant="outline" size="sm" onClick={() => void load()}>Retry</Button>
                  </div>
                )}

                {services === null || services.length > 0 ? (
                  <ServicesList
                    services={services}
                    onSelect={handleSelectService}
                    onDeleted={handleDeletedService}
                    isAdmin={can('services.manage')}
                  />
                ) : null}
              </>
            )}
          </Suspense>
        </ScreenErrorBoundary>
      </AppShell>
      <Toaster />
    </>
  );
}
