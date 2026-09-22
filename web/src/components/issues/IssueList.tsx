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
import type { Issue } from '@/lib/api.ts';
import { formatTime, timeAgo } from '@/lib/format.ts';

const pageSize = 15;

export default function IssueList({
  issues,
  loading,
  selectedId,
  showService,
  onSelect,
}: {
  issues: Issue[];
  loading: boolean;
  selectedId: number | null;
  showService: boolean;
  onSelect: (id: number) => void;
}) {
  const [query, setQuery] = useState('');
  const [release, setRelease] = useState('all');
  const [page, setPage] = useState(0);

  const releases = useMemo(() => {
    const seen = new Set<string>();

    for (const issue of issues) {
      for (const value of issue.releases) {
        seen.add(value);
      }
    }

    return [...seen].sort();
  }, [issues]);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();

    return issues.filter((issue) => {
      if (release !== 'all' && !issue.releases.includes(release)) {
        return false;
      }

      return (
        needle === '' ||
        issue.title.toLowerCase().includes(needle) ||
        issue.fingerprint.toLowerCase().includes(needle)
      );
    });
  }, [issues, query, release]);

  const pageCount = Math.max(1, Math.ceil(filtered.length / pageSize));
  const safePage = Math.min(page, pageCount - 1);
  const rows = filtered.slice(safePage * pageSize, safePage * pageSize + pageSize);
  const columnCount = (showService ? 5 : 4) + 1;

  return (
    <Card>
      <CardContent className="space-y-3 pt-4">
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input
            placeholder="Search title or fingerprint…"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setPage(0);
            }}
            className="sm:max-w-sm"
            aria-label="Search issues"
          />
          <Select
            value={release}
            onValueChange={(value) => {
              setRelease(value);
              setPage(0);
            }}
          >
            <SelectTrigger className="sm:w-48" aria-label="Filter by release">
              <SelectValue placeholder="Release" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All releases</SelectItem>
              {releases.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        {loading ? (
          <div className="space-y-2" aria-label="Loading issues">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Title</TableHead>
                {showService && <TableHead>Service</TableHead>}
                <TableHead className="text-right">Events</TableHead>
                <TableHead>Releases</TableHead>
                <TableHead>Last seen</TableHead>
                <TableHead className="text-right">Action</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((issue) => (
                <TableRow
                  key={issue.id}
                  data-state={selectedId === issue.id ? 'selected' : undefined}
                  onClick={() => onSelect(issue.id)}
                  className="cursor-pointer"
                >
                  <TableCell className="max-w-md">
                    <span className="block truncate font-medium" title={issue.title}>
                      {issue.title}
                    </span>
                    <span className="text-muted-foreground block font-mono text-[11px]">
                      {issue.fingerprint.slice(0, 16)}… · first {formatTime(issue.first_seen)}
                    </span>
                  </TableCell>
                  {showService && <TableCell className="font-mono text-xs">{issue.service_id}</TableCell>}
                  <TableCell className="text-right font-mono">{issue.count}</TableCell>
                  <TableCell>
                    <span className="flex flex-wrap gap-1">
                      {issue.releases.slice(0, 3).map((value) => (
                        <Badge key={value} variant="outline" className="font-mono text-[10px]">
                          {value}
                        </Badge>
                      ))}
                      {issue.releases.length === 0 && (
                        <span className="text-muted-foreground text-xs">—</span>
                      )}
                    </span>
                  </TableCell>
                  <TableCell className="text-muted-foreground whitespace-nowrap text-xs">
                    {timeAgo(issue.last_seen)}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={(event) => {
                        event.stopPropagation();
                        onSelect(issue.id);
                      }}
                      aria-label={`Open issue ${issue.id}`}
                    >
                      Open
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={columnCount} className="text-muted-foreground text-center">
                    No issues. Ship it.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}

        <div className="flex items-center justify-between text-sm">
          <p className="text-muted-foreground">
            {filtered.length} issue{filtered.length === 1 ? '' : 's'}
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
