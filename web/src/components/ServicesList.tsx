import { useMemo, useState } from 'react';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import type { ServiceView } from '@/lib/api.ts';

const pageSize = 10;

const healthOptions = ['all', 'healthy', 'unhealthy', 'unknown'] as const;

export default function ServicesList({
  services,
  onSelect,
}: {
  services: ServiceView[] | null;
  onSelect: (id: string) => void;
}) {
  const [query, setQuery] = useState('');
  const [health, setHealth] = useState<(typeof healthOptions)[number]>('all');
  const [page, setPage] = useState(0);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();

    return (services ?? []).filter((service) => {
      if (health !== 'all' && service.health !== health) {
        return false;
      }

      return needle === '' || service.name.toLowerCase().includes(needle);
    });
  }, [query, health, services]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = filtered.slice(safePage * pageSize, safePage * pageSize + pageSize);

  return (
    <Card>
      <CardContent className="space-y-4 pt-4">
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input
            placeholder="Search services…"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setPage(0);
            }}
            className="sm:max-w-xs"
            aria-label="Search services"
          />
          <Select
            value={health}
            onValueChange={(value) => {
              setHealth(value as (typeof healthOptions)[number]);
              setPage(0);
            }}
          >
            <SelectTrigger className="sm:w-44" aria-label="Filter by health">
              <SelectValue placeholder="Health" />
            </SelectTrigger>
            <SelectContent>
              {healthOptions.map((option) => (
                <SelectItem key={option} value={option}>
                  {option === 'all' ? 'All health states' : option}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {services === null ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Health</TableHead>
                <TableHead>Strategy</TableHead>
                <TableHead>Live</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((service) => (
                <TableRow key={service.id}>
                  <TableCell className="font-medium">{service.name}</TableCell>
                  <TableCell>
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
                  </TableCell>
                  <TableCell>{service.strategy}</TableCell>
                  <TableCell>{service.live_color === '' ? '—' : service.live_color}</TableCell>
                  <TableCell className="text-right">
                    <Button variant="outline" size="sm" onClick={() => onSelect(service.id)}>
                      Open
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground text-center">
                    No services match these filters.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}

        <div className="flex items-center justify-between text-sm">
          <p className="text-muted-foreground">
            {filtered.length} service{filtered.length === 1 ? '' : 's'}
          </p>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={safePage === 0}
              onClick={() => setPage(safePage - 1)}
            >
              Previous
            </Button>
            <span className="text-muted-foreground">
              {safePage + 1} / {pageCount}
            </span>
            <Button
              variant="outline"
              size="sm"
              disabled={safePage + 1 >= pageCount}
              onClick={() => setPage(safePage + 1)}
            >
              Next
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
