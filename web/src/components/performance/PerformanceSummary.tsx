import { Activity, Clock3, Gauge, Route, TriangleAlert } from 'lucide-react';

import { Card, CardContent } from '@/components/ui/card';
import { Skeleton } from '@/components/ui/skeleton';
import type { PerformanceRouteRow, PerformanceSummaryView } from '@/lib/api.ts';
import { formatPercentile } from '@/lib/performance.ts';

function requestRate(summary: PerformanceSummaryView): string {
  const durationMinutes =
    (new Date(summary.end).getTime() - new Date(summary.start).getTime()) / 60_000;

  if (!Number.isFinite(durationMinutes) || durationMinutes <= 0) {
    return '—';
  }

  return (summary.request_count / durationMinutes).toLocaleString(undefined, {
    maximumFractionDigits: 1,
  });
}

function SummaryCard({
  label,
  value,
  detail,
  icon: Icon,
}: {
  label: string;
  value: string;
  detail: string;
  icon: typeof Activity;
}) {
  return (
    <Card>
      <CardContent className="space-y-2 p-4">
        <div className="text-muted-foreground flex items-center gap-2 text-xs font-medium">
          <Icon className="h-4 w-4" aria-hidden />
          {label}
        </div>
        <p className="text-2xl font-semibold tracking-tight">{value}</p>
        <p className="text-muted-foreground text-xs">{detail}</p>
      </CardContent>
    </Card>
  );
}

export function PerformanceSummarySkeleton() {
  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5" aria-label="Loading performance summary">
      {Array.from({ length: 5 }, (_, index) => (
        <Card key={index}>
          <CardContent className="space-y-3 p-4">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-8 w-20" />
            <Skeleton className="h-3 w-32" />
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

export default function PerformanceSummary({
  summary,
  routes,
}: {
  summary: PerformanceSummaryView;
  routes: PerformanceRouteRow[];
}) {
  const slowRoutes = routes.filter((route) => route.p95_ms >= 1_000).length;

  return (
    <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
      <SummaryCard
        label="Requests / min"
        value={requestRate(summary)}
        detail={`${summary.request_count.toLocaleString()} in range`}
        icon={Activity}
      />
      <SummaryCard
        label="p50 latency"
        value={formatPercentile(summary.p50_ms)}
        detail="Typical response"
        icon={Clock3}
      />
      <SummaryCard
        label="p95 / p99"
        value={`${formatPercentile(summary.p95_ms)} / ${formatPercentile(summary.p99_ms)}`}
        detail="Tail response time"
        icon={Gauge}
      />
      <SummaryCard
        label="Error rate"
        value={`${summary.error_rate.toFixed(2)}%`}
        detail={`${summary.error_count.toLocaleString()} HTTP errors`}
        icon={TriangleAlert}
      />
      <SummaryCard
        label="Slow routes"
        value={String(slowRoutes)}
        detail="p95 at or above 1 second"
        icon={Route}
      />
    </div>
  );
}
