import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts';

import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart';
import type { HistoryPoint } from '@/lib/api.ts';
import { formatBytes } from '@/lib/format.ts';

const cpuConfig = {
  cpu: { label: 'CPU %', color: 'var(--chart-1)' },
} satisfies ChartConfig;

const memConfig = {
  mem: { label: 'Memory', color: 'var(--chart-2)' },
} satisfies ChartConfig;

const loadConfig = {
  load: { label: 'Load (1m)', color: 'var(--chart-3)' },
} satisfies ChartConfig;

function formatTick(ts: string, hours: number): string {
  const parsed = new Date(ts);

  if (Number.isNaN(parsed.getTime())) {
    return ts;
  }

  const time = parsed.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

  if (hours <= 24) {
    return time;
  }

  return `${parsed.getMonth() + 1}/${parsed.getDate()} ${time}`;
}

function toSeries(points: HistoryPoint[], hours: number): { at: string; value: number }[] {
  return [...points]
    .sort((a, b) => (a.ts < b.ts ? -1 : a.ts > b.ts ? 1 : 0))
    .map((point) => ({ at: formatTick(point.ts, hours), value: point.value }));
}

export default function FleetHistoryCharts({ cpu, mem, load, range }: {
  cpu: HistoryPoint[];
  mem: HistoryPoint[];
  load: HistoryPoint[];
  range: number;
}) {
  const cpuSeries = toSeries(cpu, range);
  const memSeries = toSeries(mem, range);
  const loadSeries = toSeries(load, range);

  return (
    <div className="grid gap-4 lg:grid-cols-3">
      <div>
        <p className="text-muted-foreground mb-1 text-xs">CPU %</p>
        {cpuSeries.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">
            No CPU samples in range.
          </p>
        ) : (
          <ChartContainer config={cpuConfig} className="h-44 w-full">
            <LineChart data={cpuSeries}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
              <YAxis tickLine={false} axisLine={false} width={40} />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Line type="monotone" dataKey="value" stroke="var(--color-cpu)" dot={false} />
            </LineChart>
          </ChartContainer>
        )}
      </div>
      <div>
        <p className="text-muted-foreground mb-1 text-xs">Memory used</p>
        {memSeries.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">
            No memory samples in range.
          </p>
        ) : (
          <ChartContainer config={memConfig} className="h-44 w-full">
            <LineChart data={memSeries}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={52}
                tickFormatter={(value: number) => formatBytes(value)}
              />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Line type="monotone" dataKey="value" stroke="var(--color-mem)" dot={false} />
            </LineChart>
          </ChartContainer>
        )}
      </div>
      <div>
        <p className="text-muted-foreground mb-1 text-xs">Load (1m)</p>
        {loadSeries.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">
            No load samples in range.
          </p>
        ) : (
          <ChartContainer config={loadConfig} className="h-44 w-full">
            <LineChart data={loadSeries}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="at" tickLine={false} axisLine={false} minTickGap={32} />
              <YAxis tickLine={false} axisLine={false} width={36} />
              <ChartTooltip content={<ChartTooltipContent />} />
              <Line type="monotone" dataKey="value" stroke="var(--color-load)" dot={false} />
            </LineChart>
          </ChartContainer>
        )}
      </div>
    </div>
  );
}
