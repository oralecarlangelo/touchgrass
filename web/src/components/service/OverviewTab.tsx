import { useCallback, useEffect, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from '../ErrorState.tsx';
import {
  fetchDeploys,
  fetchIssues,
  fetchNotifications,
  isUnauthorized,
  type Deploy,
  type Notification,
  type ServiceView,
} from '@/lib/api.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

export default function OverviewTab({
  service,
  onUnauthorized,
}: {
  service: ServiceView;
  onUnauthorized: () => void;
}) {
  const [deploys, setDeploys] = useState<Deploy[] | null>(null);
  const [notes, setNotes] = useState<Notification[] | null>(null);
  const [issueCount, setIssueCount] = useState<number | null>(null);
  const [eventCount, setEventCount] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      const [fetchedDeploys, fetchedNotes, fetchedIssues] = await Promise.all([
        fetchDeploys(service.id),
        fetchNotifications(service.id, 5),
        fetchIssues(service.id),
      ]);
      setDeploys(fetchedDeploys);
      setNotes(fetchedNotes.notifications);
      setIssueCount(fetchedIssues.length);
      setEventCount(fetchedIssues.reduce((sum, issue) => sum + issue.count, 0));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [service.id, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  if (error !== null) {
    return <ErrorState title={`Couldn't load ${service.name}`} message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-3">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Health</CardTitle>
          </CardHeader>
          <CardContent>
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
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Issues / events</CardTitle>
          </CardHeader>
          <CardContent>
            {issueCount === null || eventCount === null ? (
              <Skeleton className="h-6 w-20" />
            ) : (
              <p className="text-2xl font-semibold">
                {issueCount}
                <span className="text-muted-foreground text-base font-normal">
                  {' '}
                  / {eventCount} events
                </span>
              </p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Last deploy</CardTitle>
          </CardHeader>
          <CardContent>
            {deploys === null ? (
              <Skeleton className="h-6 w-28" />
            ) : deploys.length === 0 ? (
              <p className="text-muted-foreground text-sm">Nothing recorded</p>
            ) : (
              <p className="text-sm">
                <span className="font-mono">{deploys[0].sha.slice(0, 12)}</span>{' '}
                <span className="text-muted-foreground">by {deploys[0].actor}</span>
              </p>
            )}
          </CardContent>
        </Card>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-medium">
              Containers ({service.containers.length})
            </CardTitle>
          </CardHeader>
          <CardContent>
            {service.containers.length === 0 ? (
              <p className="text-muted-foreground text-sm">No containers reported.</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Image</TableHead>
                    <TableHead>State</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {service.containers.map((container) => (
                    <TableRow key={container.id}>
                      <TableCell className="font-medium">{container.name}</TableCell>
                      <TableCell
                        className="max-w-56 truncate font-mono text-xs"
                        title={container.image}
                      >
                        {container.image}
                      </TableCell>
                      <TableCell>
                        <Badge variant={container.state === 'running' ? 'default' : 'secondary'}>
                          {container.state}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-medium">Recent notifications</CardTitle>
          </CardHeader>
          <CardContent>
            {notes === null ? (
              <div className="space-y-2">
                <Skeleton className="h-6 w-full" />
                <Skeleton className="h-6 w-full" />
              </div>
            ) : notes.length === 0 ? (
              <p className="text-muted-foreground text-sm">Quiet. Prod is covered.</p>
            ) : (
              <ul className="space-y-2">
                {notes.map((item) => (
                  <li key={item.id} className="flex items-start gap-2 text-sm">
                    {!item.read_at && (
                      <span
                        className="bg-primary mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full"
                        aria-hidden
                      />
                    )}
                    <span className="min-w-0 flex-1">
                      <span className="block font-medium">{item.title}</span>
                      <span className="text-muted-foreground block text-xs">
                        {item.kind} · {timeAgo(item.created_at)}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      {service.colors.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-medium">Colors</CardTitle>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Color</TableHead>
                  <TableHead>Health</TableHead>
                  <TableHead>Target</TableHead>
                  <TableHead>Health URL</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {service.colors.map((color) => (
                  <TableRow key={color.name}>
                    <TableCell className="font-medium">
                      {color.name}
                      {color.live && (
                        <Badge variant="default" className="ml-2">
                          live
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell>{color.health}</TableCell>
                    <TableCell className="font-mono text-xs">{color.target}</TableCell>
                    <TableCell className="font-mono text-xs">{color.health_url}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium">Latest deploys</CardTitle>
        </CardHeader>
        <CardContent>
          {deploys === null ? (
            <Skeleton className="h-10 w-full" />
          ) : deploys.length === 0 ? (
            <p className="text-muted-foreground text-sm">Nothing recorded yet.</p>
          ) : (
            <ul className="divide-y divide-border">
              {deploys.slice(0, 5).map((deploy) => (
                <li key={deploy.id} className="flex flex-wrap items-center gap-2 py-2 text-sm">
                  <span className="font-mono">{deploy.sha.slice(0, 12)}</span>
                  <span className="text-muted-foreground">by {deploy.actor}</span>
                  <Badge variant="outline">{deploy.type}</Badge>
                  <Badge variant={deploy.outcome === 'success' ? 'default' : 'destructive'}>
                    {deploy.outcome}
                  </Badge>
                  <span className="text-muted-foreground ml-auto text-xs">
                    {formatTime(deploy.created_at)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
