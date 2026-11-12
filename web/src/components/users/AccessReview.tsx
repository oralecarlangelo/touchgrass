import { useCallback, useEffect, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import {
  grantOf,
  hostReach,
  matrixSections,
  serviceReach,
  unassigned,
  type Reach,
} from '@/lib/access-review.ts';
import {
  fetchAccessMatrix,
  isUnauthorized,
  type AccessMatrix,
  type ManagedUser,
  type ServiceView,
} from '@/lib/api.ts';

function reachTitle(account: Reach): string {
  const how = account.viaAllServices ? 'has every service' : 'granted this service';

  return account.disabled
    ? `${account.username}: ${account.role}, ${how}. Disabled: cannot sign in.`
    : `${account.username}: ${account.role}, ${how}.`;
}

// One account in a "who can reach it" cell: name, role, and whether the
// service came with an all-services scope.
function ReachBadge({ account }: { account: Reach }) {
  return (
    <Badge
      variant={account.role === 'admin' ? 'default' : account.role === 'developer' ? 'secondary' : 'outline'}
      className={account.disabled ? 'line-through opacity-60' : undefined}
      title={reachTitle(account)}
    >
      {account.username}
      <span className="font-normal opacity-80">
        {' · '}
        {account.role}
        {account.viaAllServices ? ' · all' : ''}
        {account.disabled ? ' · disabled' : ''}
      </span>
    </Badge>
  );
}

function ReachList({ accounts, empty }: { accounts: Reach[]; empty: string }) {
  if (accounts.length === 0) {
    return <span className="text-muted-foreground text-sm">{empty}</span>;
  }

  return (
    <div className="flex flex-wrap gap-1">
      {accounts.map((account) => (
        <ReachBadge key={account.username} account={account} />
      ))}
    </div>
  );
}

// ServiceReachTable is the users-by-services view: for each service,
// every account that can reach it and with which role.
export function ServiceReachTable({
  services,
  users,
}: {
  services: { id: string; name: string }[];
  users: ManagedUser[];
}) {
  const reach = serviceReach(services, users);
  const nobody = unassigned(users);

  return (
    <div className="space-y-3">
      <p className="text-muted-foreground text-sm">
        Each account is shown with its role. “all” marks an account that has every service, including
        ones added later; the others were granted the service by name.
      </p>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-1/4">Service</TableHead>
            <TableHead>Who can reach it</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {reach.map((entry) => (
            <TableRow key={entry.serviceId}>
              <TableCell className="align-top font-medium">
                {entry.serviceName}
                {entry.serviceName !== entry.serviceId && (
                  <span className="text-muted-foreground block font-mono text-xs font-normal">
                    {entry.serviceId}
                  </span>
                )}
              </TableCell>
              <TableCell className="whitespace-normal">
                <ReachList accounts={entry.accounts} empty="Nobody" />
              </TableCell>
            </TableRow>
          ))}
          <TableRow>
            <TableCell className="align-top font-medium">
              Host screens
              <span className="text-muted-foreground block text-xs font-normal">
                Fleet, Security, Databases, Images, API keys, full audit trail
              </span>
            </TableCell>
            <TableCell className="whitespace-normal">
              <ReachList accounts={hostReach(users)} empty="Nobody" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      {reach.length === 0 && (
        <p className="text-muted-foreground text-sm">No services are managed yet.</p>
      )}
      {nobody.length > 0 && (
        <div className="space-y-1">
          <p className="text-sm font-medium">No services assigned</p>
          <p className="text-muted-foreground text-sm">
            These accounts can sign in and see nothing until you grant them a service.
          </p>
          <ReachList accounts={nobody} empty="" />
        </div>
      )}
    </div>
  );
}

const grantText = { yes: 'Yes', limited: 'Limited', no: '—' } as const;

// RoleMatrixTable renders the server's policy: what each role may do,
// grouped by where it applies.
export function RoleMatrixTable({ matrix }: { matrix: AccessMatrix }) {
  return (
    <div className="space-y-5">
      {matrixSections(matrix).map((section) => (
        <section key={section.scope} className="space-y-2">
          <div>
            <h3 className="text-sm font-medium">{section.title}</h3>
            <p className="text-muted-foreground text-sm">{section.explanation}</p>
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>What</TableHead>
                {matrix.roles.map((role) => (
                  <TableHead key={role} className="w-24 text-center capitalize">
                    {role}
                  </TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {section.rows.map((row) => (
                <TableRow key={row.capability}>
                  <TableCell className="whitespace-normal">
                    {row.summary}
                    <span className="text-muted-foreground block font-mono text-xs">{row.capability}</span>
                    {matrix.roles
                      .filter((role) => grantOf(row, role) === 'limited')
                      .map((role) => (
                        <span key={role} className="text-muted-foreground mt-1 block text-xs">
                          <span className="text-foreground font-medium capitalize">{role}:</span>{' '}
                          {row.limits[role]}
                        </span>
                      ))}
                  </TableCell>
                  {matrix.roles.map((role) => {
                    const grant = grantOf(row, role);

                    return (
                      <TableCell
                        key={role}
                        className={
                          grant === 'no'
                            ? 'text-muted-foreground text-center align-top'
                            : 'text-center align-top font-medium'
                        }
                        aria-label={`${role}: ${grant === 'no' ? 'no' : grantText[grant]}`}
                      >
                        {grantText[grant]}
                      </TableCell>
                    );
                  })}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </section>
      ))}
    </div>
  );
}

// AccessReview answers "who can do what": the accounts that reach each
// service, and what each role may do there. The matrix comes from the
// server's own policy table, so it cannot drift from what is enforced.
export default function AccessReview({
  services,
  users,
  onUnauthorized,
}: {
  services: ServiceView[];
  users: ManagedUser[];
  onUnauthorized: () => void;
}) {
  const [matrix, setMatrix] = useState<AccessMatrix | null>(null);
  const [matrixError, setMatrixError] = useState<string | null>(null);

  const loadMatrix = useCallback(async () => {
    setMatrixError(null);

    try {
      setMatrix(await fetchAccessMatrix());
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setMatrixError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  useEffect(() => {
    void loadMatrix();
  }, [loadMatrix]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium">Access review: who can do what</CardTitle>
      </CardHeader>
      <CardContent>
        <Tabs defaultValue="services">
          <TabsList>
            <TabsTrigger value="services">By service</TabsTrigger>
            <TabsTrigger value="roles">What each role can do</TabsTrigger>
          </TabsList>
          <TabsContent value="services" className="pt-2">
            <ServiceReachTable services={services} users={users} />
          </TabsContent>
          <TabsContent value="roles" className="pt-2">
            {matrixError !== null ? (
              <div role="alert" className="space-y-2 text-sm">
                <p className="text-destructive">Couldn&apos;t load the access matrix: {matrixError}</p>
                <Button variant="outline" size="sm" onClick={() => void loadMatrix()}>
                  Retry
                </Button>
              </div>
            ) : matrix === null ? (
              <div className="space-y-2" aria-label="Loading the access matrix">
                <Skeleton className="h-10 w-full" />
              </div>
            ) : (
              <RoleMatrixTable matrix={matrix} />
            )}
          </TabsContent>
        </Tabs>
      </CardContent>
    </Card>
  );
}
