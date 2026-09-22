import { useCallback, useEffect, useState } from 'react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
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
import ErrorState from '../ErrorState.tsx';
import {
  createIssueRule,
  createRule,
  deleteIssueRule,
  deleteRule,
  fetchIssueRules,
  fetchRules,
  isUnauthorized,
  type AlertRule,
  type IssueRule,
} from '@/lib/api.ts';

export default function AlertsTab({
  serviceId,
  onUnauthorized,
}: {
  serviceId: string;
  onUnauthorized: () => void;
}) {
  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [issueRules, setIssueRules] = useState<IssueRule[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dialog, setDialog] = useState<'metric' | 'issue' | null>(null);
  const [metric, setMetric] = useState('mem');
  const [threshold, setThreshold] = useState('85');
  const [duration, setDuration] = useState('300');
  const [issueKind, setIssueKind] = useState('spike');
  const [issueThreshold, setIssueThreshold] = useState('10');
  const [issueWindow, setIssueWindow] = useState('300');
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<{ kind: 'metric' | 'issue'; id: number } | null>(
    null,
  );

  function openDialog(kind: 'metric' | 'issue'): void {
    setFormError(null);
    setDialog(kind);
  }

  const load = useCallback(async () => {
    setError(null);

    try {
      const [fetchedRules, fetchedIssueRules] = await Promise.all([
        fetchRules(serviceId),
        fetchIssueRules(serviceId),
      ]);
      setRules(fetchedRules);
      setIssueRules(fetchedIssueRules);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [serviceId, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  async function handleCreateMetricRule(): Promise<void> {
    const thresholdValue = Number.parseFloat(threshold);
    const durationValue = Number.parseInt(duration, 10);

    if (!Number.isFinite(thresholdValue) || thresholdValue <= 0) {
      setFormError('Threshold must be a positive number.');
      return;
    }

    if (!Number.isInteger(durationValue) || durationValue <= 0) {
      setFormError('Duration must be a positive whole number of seconds.');
      return;
    }

    setFormError(null);

    setSaving(true);

    try {
      await createRule({
        service_id: serviceId,
        metric,
        threshold: thresholdValue,
        duration_secs: durationValue,
      });
      setDialog(null);
      setThreshold('85');
      setDuration('300');
      toast.success('Alert rule created.');
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  async function handleCreateIssueRule(): Promise<void> {
    const thresholdValue = Number.parseFloat(issueThreshold);
    const windowValue = Number.parseInt(issueWindow, 10);

    if (!Number.isFinite(thresholdValue) || thresholdValue <= 0) {
      setFormError('Threshold must be a positive number.');
      return;
    }

    if (!Number.isInteger(windowValue) || windowValue <= 0) {
      setFormError('Window must be a positive whole number of seconds.');
      return;
    }

    setFormError(null);

    setSaving(true);

    try {
      await createIssueRule({
        service_id: serviceId,
        kind: issueKind,
        threshold: thresholdValue,
        window_secs: windowValue,
      });
      setDialog(null);
      toast.success('Issue rule created.');
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(): Promise<void> {
    if (pendingDelete === null) {
      return;
    }

    try {
      if (pendingDelete.kind === 'metric') {
        await deleteRule(pendingDelete.id);
      } else {
        await deleteIssueRule(pendingDelete.id);
      }

      toast.success('Rule deleted.');
      setPendingDelete(null);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    }
  }

  if (error !== null) {
    return <ErrorState title="Couldn't load alert rules" message={error} onRetry={() => void load()} />;
  }

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-sm font-medium">Metric rules</CardTitle>
          <Button size="sm" onClick={() => openDialog('metric')}>
            New rule
          </Button>
        </CardHeader>
        <CardContent>
          {rules === null ? (
            <Skeleton className="h-10 w-full" />
          ) : rules.length === 0 ? (
            <p className="text-muted-foreground text-sm">
              No rules — breaches will go unnoticed.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Condition</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rules.map((rule) => (
                  <TableRow key={rule.id}>
                    <TableCell className="font-mono text-xs">
                      {rule.metric} &gt; {rule.threshold} for {rule.duration_secs}s
                    </TableCell>
                    <TableCell>
                      <Badge variant={rule.enabled ? 'default' : 'secondary'}>
                        {rule.enabled ? 'enabled' : 'disabled'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => setPendingDelete({ kind: 'metric', id: rule.id })}
                        aria-label={`Delete rule ${rule.id}`}
                      >
                        Delete
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-sm font-medium">Issue rules</CardTitle>
          <Button size="sm" onClick={() => openDialog('issue')}>
            New rule
          </Button>
        </CardHeader>
        <CardContent>
          {issueRules === null ? (
            <Skeleton className="h-10 w-full" />
          ) : issueRules.length === 0 ? (
            <p className="text-muted-foreground text-sm">
              No issue rules — error spikes won&apos;t page anyone.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Condition</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {issueRules.map((rule) => (
                  <TableRow key={rule.id}>
                    <TableCell className="font-mono text-xs">
                      {rule.kind} &gt; {rule.threshold} / {rule.window_secs}s
                    </TableCell>
                    <TableCell>
                      <Badge variant={rule.enabled ? 'default' : 'secondary'}>
                        {rule.enabled ? 'enabled' : 'disabled'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-destructive hover:text-destructive"
                        onClick={() => setPendingDelete({ kind: 'issue', id: rule.id })}
                        aria-label={`Delete issue rule ${rule.id}`}
                      >
                        Delete
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialog !== null} onOpenChange={(open) => !open && setDialog(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{dialog === 'issue' ? 'New issue rule' : 'New metric rule'}</DialogTitle>
            <DialogDescription>
              {dialog === 'issue'
                ? 'Notify when error events cross a threshold inside a window.'
                : 'Notify when a container metric stays over threshold.'}
            </DialogDescription>
          </DialogHeader>
          {dialog === 'metric' ? (
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="space-y-1">
                <Label htmlFor="rule-metric">Metric</Label>
                <Select value={metric} onValueChange={setMetric}>
                  <SelectTrigger id="rule-metric">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="cpu">cpu %</SelectItem>
                    <SelectItem value="mem">mem %</SelectItem>
                    <SelectItem value="disk">disk bytes</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label htmlFor="rule-threshold">Over</Label>
                <Input
                  id="rule-threshold"
                  value={threshold}
                  onChange={(event) => setThreshold(event.target.value)}
                  inputMode="decimal"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="rule-duration">For (s)</Label>
                <Input
                  id="rule-duration"
                  value={duration}
                  onChange={(event) => setDuration(event.target.value)}
                  inputMode="numeric"
                />
              </div>
            </div>
          ) : (
            <div className="grid gap-3 sm:grid-cols-3">
              <div className="space-y-1">
                <Label htmlFor="issue-kind">Kind</Label>
                <Select value={issueKind} onValueChange={setIssueKind}>
                  <SelectTrigger id="issue-kind">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="spike">event spike</SelectItem>
                    <SelectItem value="new_issue">new issue</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label htmlFor="issue-threshold">Over</Label>
                <Input
                  id="issue-threshold"
                  value={issueThreshold}
                  onChange={(event) => setIssueThreshold(event.target.value)}
                  inputMode="decimal"
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="issue-window">Window (s)</Label>
                <Input
                  id="issue-window"
                  value={issueWindow}
                  onChange={(event) => setIssueWindow(event.target.value)}
                  inputMode="numeric"
                />
              </div>
            </div>
          )}
          {formError !== null && (
            <p role="alert" className="text-destructive text-sm">
              {formError}
            </p>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setDialog(null)}>
              Cancel
            </Button>
            <Button
              onClick={() => void (dialog === 'issue' ? handleCreateIssueRule() : handleCreateMetricRule())}
              disabled={saving}
            >
              {saving ? 'Creating…' : 'Create rule'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={pendingDelete !== null} onOpenChange={(open) => !open && setPendingDelete(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete this rule?</DialogTitle>
            <DialogDescription>
              Breaches this rule would have caught will go unnoticed. This can&apos;t be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPendingDelete(null)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void handleDelete()}>
              Delete rule
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
