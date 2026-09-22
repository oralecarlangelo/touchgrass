import { useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import HistoryScreen from '../HistoryScreen.tsx';
import LogSearch from '../logs/LogSearch.tsx';
import ActivityTab from './ActivityTab.tsx';
import AlertsTab from './AlertsTab.tsx';
import DeploysTab from './DeploysTab.tsx';
import IssuesTab from './IssuesTab.tsx';
import MetricsTab from './MetricsTab.tsx';
import OverviewTab from './OverviewTab.tsx';
import type { ServiceView } from '@/lib/api.ts';

type Tab = 'overview' | 'deploys' | 'metrics' | 'logs' | 'activity' | 'issues' | 'alerts' | 'audit';

export default function ServiceWorkspace({
  service,
  onBack,
  onServiceChanged,
  onUnauthorized,
}: {
  service: ServiceView;
  onBack: () => void;
  onServiceChanged: () => void;
  onUnauthorized: () => void;
}) {
  const [tab, setTab] = useState<Tab>('overview');

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

      <Tabs value={tab} onValueChange={(value) => setTab(value as Tab)}>
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="deploys">Deploys</TabsTrigger>
          <TabsTrigger value="metrics">Metrics</TabsTrigger>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="activity">Activity</TabsTrigger>
          <TabsTrigger value="issues">Issues</TabsTrigger>
          <TabsTrigger value="alerts">Alerts</TabsTrigger>
          <TabsTrigger value="audit">Audit</TabsTrigger>
        </TabsList>

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
            />
          )}
        </TabsContent>
        <TabsContent value="metrics">
          {tab === 'metrics' && (
            <MetricsTab serviceId={service.id} onUnauthorized={onUnauthorized} />
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
            <AlertsTab serviceId={service.id} onUnauthorized={onUnauthorized} />
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
