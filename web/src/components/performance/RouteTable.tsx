import { Route } from 'lucide-react';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { PerformanceRouteRow } from '@/lib/api.ts';
import { formatPercentile } from '@/lib/performance.ts';

function p95Change(route: PerformanceRouteRow): string {
  if (route.previous.p95_ms <= 0) {
    return 'new';
  }

  const change = ((route.p95_ms - route.previous.p95_ms) / route.previous.p95_ms) * 100;

  return `${change >= 0 ? '+' : ''}${change.toFixed(0)}%`;
}

export default function RouteTable({ routes }: { routes: PerformanceRouteRow[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Route className="h-4 w-4" aria-hidden />
          Route families
        </CardTitle>
        <CardDescription>Privacy-safe route groups, compared with the preceding window.</CardDescription>
      </CardHeader>
      <CardContent>
        {routes.length === 0 ? (
          <p className="text-muted-foreground rounded-md border border-dashed p-6 text-center text-sm">
            No approved HTTP route families were recorded in this range.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Route</TableHead>
                  <TableHead className="text-right">Requests</TableHead>
                  <TableHead className="text-right">p50</TableHead>
                  <TableHead className="text-right">p95</TableHead>
                  <TableHead className="text-right">p99</TableHead>
                  <TableHead className="text-right">Errors</TableHead>
                  <TableHead className="text-right">p95 change</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {routes.map((route) => (
                  <TableRow key={`${route.method}:${route.route_family}`}>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Badge variant="outline">{route.method}</Badge>
                        <code className="text-xs">{route.route_family}</code>
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {route.request_count.toLocaleString()}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {formatPercentile(route.p50_ms)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {formatPercentile(route.p95_ms)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {formatPercentile(route.p99_ms)}
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs">
                      {route.error_rate.toFixed(2)}%
                    </TableCell>
                    <TableCell className="text-right">
                      <Badge variant={route.p95_ms > route.previous.p95_ms ? 'destructive' : 'secondary'}>
                        {p95Change(route)}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
