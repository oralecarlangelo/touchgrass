import { useEffect, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import HistoryScreen from '../HistoryScreen.tsx';
import LogSearch from '../logs/LogSearch.tsx';
import ActivityTab from './ActivityTab.tsx';
import AlertsTab from './AlertsTab.tsx';
import DeploysTab from './DeploysTab.tsx';
import EnvironmentTab from './EnvironmentTab.tsx';
import IssuesTab from './IssuesTab.tsx';
import MigrationsTab from './MigrationsTab.tsx';
import OverviewTab from './OverviewTab.tsx';
import PerformanceTab from './PerformanceTab.tsx';
import { envAccessFor, recordsAsSelf, type Can } from '@/lib/access.ts';
import type { ServiceView, SessionInfo } from '@/lib/api.ts';

export type ServiceTab =
  | 'overview'
  | 'deploys'
  | 'environment'
  | 'migrations'
  | 'performance'
  | 'logs'
  | 'activity'
  | 'issues'
  | 'alerts'
  | 'audit';

export default function ServiceWorkspace({
  service,
  onBack,
  onServiceChanged,
  onUnauthorized,
  can,
  session,
  initialTab = 'overview',
  onTabChange,
}: {
  service: ServiceView;
  onBack: () => void;
  onServiceChanged: () => void;
  onUnauthorized: () => void;
  can: Can;
  session: SessionInfo;
  initialTab?: ServiceTab;
  onTabChange?: (tab: ServiceTab) => void;
}) {
  // Migration status is a developer and admin view; a viewer has no
  // tab, and a link to it lands on the overview.
  const showMigrations = can('migrations.read', service.id);
  const startTab = initialTab === 'migrations' && !showMigrations ? 'overview' : initialTab;
  const [tab, setTab] = useState<ServiceTab>(startTab);

  useEffect(() => {
    setTab(startTab);
  }, [startTab, service.id]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="ghost" size="sm" onClick={onBack} aria-label="Back to services">
          ← Services
        </Button>
        <h1 className="text-lg font-semibold tracking-tight">{service.name}</h1>
        <Badge
          variant={
            service.health === 'healthy'
              ? 'default'
              : service.health === 'unknown'
                ? 'secondary'
                : 'destructive'
          }
        >
          {service.health}
        </Badge>
        <span className="text-muted-foreground text-sm">
          {service.strategy}
          {service.live_color !== '' && ` · live ${service.live_color}`}
        </span>
      </div>

      <Tabs value={tab} onValueChange={(value) => {
          setTab(value as ServiceTab);
          onTabChange?.(value as ServiceTab);
        }}>
        {/* Up to ten tabs outgrow a phone-width viewport; let the strip scroll instead of the page. */}
        <div className="-mx-4 overflow-x-auto px-4 sm:mx-0 sm:px-0">
          <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="deploys">Deploys</TabsTrigger>
          <TabsTrigger value="environment">Environment</TabsTrigger>
          {showMigrations && <TabsTrigger value="migrations">Migrations</TabsTrigger>}
          <TabsTrigger value="performance">Performance</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="activity">Activity</TabsTrigger>
          <TabsTrigger value="issues">Issues</TabsTrigger>
          <TabsTrigger value="alerts">Alerts</TabsTrigger>
          <TabsTrigger value="audit">Audit</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="overview">
          {tab === 'overview' && (
            <OverviewTab service={service} onUnauthorized={onUnauthorized} />
          )}
        </TabsContent>
        <TabsContent value="deploys">
          {tab === 'deploys' && (
            <DeploysTab
              service={service}
              onServiceChanged={onServiceChanged}
              onUnauthorized={onUnauthorized}
              canTrigger={can('deploy.trigger', service.id)}
              canRecord={can('deploy.record', service.id)}
              recordAs={recordsAsSelf(session) ? session.username : null}
            />
          )}
        </TabsContent>
        <TabsContent value="environment">
          {tab === 'environment' && (
            <EnvironmentTab
              service={service}
              access={envAccessFor(can, service.id, session.username)}
              onUnauthorized={onUnauthorized}
            />
          )}
        </TabsContent>
        {showMigrations && (
          <TabsContent value="migrations">
            {tab === 'migrations' && (
              <MigrationsTab
                service={service}
                canManage={can('migrations.manage', service.id)}
                onUnauthorized={onUnauthorized}
              />
            )}
          </TabsContent>
        )}
        <TabsContent value="performance">
          {tab === 'performance' && (
            <PerformanceTab serviceId={service.id} onUnauthorized={onUnauthorized} />
          )}
        </TabsContent>
        <TabsContent value="logs">
          {tab === 'logs' && <LogSearch serviceId={service.id} onUnauthorized={onUnauthorized} />}
        </TabsContent>
        <TabsContent value="activity">
          {tab === 'activity' && (
            <ActivityTab serviceId={service.id} onUnauthorized={onUnauthorized} />
          )}
        </TabsContent>
        <TabsContent value="issues">
          {tab === 'issues' && (
            <IssuesTab serviceId={service.id} onUnauthorized={onUnauthorized} />
          )}
        </TabsContent>
        <TabsContent value="alerts">
          {tab === 'alerts' && (
            <AlertsTab
              serviceId={service.id}
              onUnauthorized={onUnauthorized}
              isAdmin={can('alerts.manage', service.id)}
            />
          )}
        </TabsContent>
        <TabsContent value="audit">
          {tab === 'audit' && (
            <HistoryScreen
              services={[service]}
              fixedServiceId={service.id}
              onUnauthorized={onUnauthorized}
            />
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}
