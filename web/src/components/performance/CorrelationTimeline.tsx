import { Activity, GitCommitHorizontal } from 'lucide-react';
import { Bar, CartesianGrid, ComposedChart, Line, XAxis, YAxis } from 'recharts';

import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart';
import type { PerformanceTimelineView } from '@/lib/api.ts';
import { mergeTimelineSeries } from '@/lib/performance.ts';

const chartConfig = {
  p95_ms: { label: 'p95 latency (ms)', color: 'var(--chart-1)' },
  error_rate: { label: 'Error rate (%)', color: 'var(--chart-2)' },
  cpu_percent: { label: 'CPU (%)', color: 'var(--chart-3)' },
  postgres_connection_pct: { label: 'Postgres connections (%)', color: 'var(--chart-4)' },
} satisfies ChartConfig;

function tick(value: string): string {
  const parsed = new Date(value);

  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return parsed.toLocaleString([], {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export default function CorrelationTimeline({ timeline }: { timeline: PerformanceTimelineView }) {
  const model = mergeTimelineSeries(timeline.points, timeline.events, timeline.deployments);

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2">
              <Activity className="h-4 w-4" aria-hidden />
              Correlation timeline
            </CardTitle>
            <CardDescription>
              Latency, errors, resources, and dependency pressure aligned by bucket.
            </CardDescription>
          </div>
          <Badge variant="outline">{timeline.resolution} buckets</Badge>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {model.points.length === 0 ? (
          <div className="text-muted-foreground flex min-h-52 items-center justify-center rounded-md border border-dashed text-sm">
            No timeline samples in this range.
          </div>
        ) : (
          <ChartContainer config={chartConfig} className="min-h-64 w-full">
            <ComposedChart data={model.points} accessibilityLayer>
              <CartesianGrid vertical={false} />
              <XAxis
                dataKey="bucket_start"
                tickFormatter={tick}
                tickLine={false}
                axisLine={false}
                minTickGap={48}
              />
              <YAxis yAxisId="latency" tickLine={false} axisLine={false} width={42} />
              <YAxis
                yAxisId="percent"
                orientation="right"
                domain={[0, 100]}
                tickLine={false}
                axisLine={false}
                width={36}
              />
              <ChartTooltip
                content={<ChartTooltipContent labelFormatter={(value) => tick(String(value ?? ''))} />}
              />
              <Bar
                yAxisId="percent"
                dataKey="error_rate"
                fill="var(--color-error_rate)"
                opacity={0.35}
                radius={[3, 3, 0, 0]}
              />
              <Line
                yAxisId="latency"
                dataKey="p95_ms"
                stroke="var(--color-p95_ms)"
                strokeWidth={2}
                dot={false}
                connectNulls={false}
              />
              <Line
                yAxisId="percent"
                dataKey="cpu_percent"
                stroke="var(--color-cpu_percent)"
                strokeWidth={1.5}
                dot={false}
                connectNulls={false}
              />
              <Line
                yAxisId="percent"
                dataKey="postgres_connection_pct"
                stroke="var(--color-postgres_connection_pct)"
                strokeDasharray="4 3"
                strokeWidth={1.5}
                dot={false}
                connectNulls={false}
              />
            </ComposedChart>
          </ChartContainer>
        )}

        <div className="border-t pt-3">
          <p className="text-muted-foreground mb-2 flex items-center gap-2 text-xs font-medium">
            <GitCommitHorizontal className="h-3.5 w-3.5" aria-hidden />
            Changes in range
          </p>
          {model.markers.length === 0 ? (
            <p className="text-muted-foreground text-xs">No deployments or runtime changes recorded.</p>
          ) : (
            <div className="flex gap-2 overflow-x-auto pb-1">
              {model.markers.map((marker) => (
                <div key={marker.id} className="bg-muted min-w-44 rounded-md px-3 py-2 text-xs">
                  <div className="flex items-center justify-between gap-2">
                    <Badge variant={marker.expected ? 'secondary' : 'destructive'}>
                      {marker.kind}
                    </Badge>
                    <time className="text-muted-foreground" dateTime={marker.at}>
                      {tick(marker.at)}
                    </time>
                  </div>
                  <p className="mt-2 font-medium">{marker.label}</p>
                  {marker.serviceId !== '' && (
                    <p className="text-muted-foreground mt-1">{marker.serviceId}</p>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
