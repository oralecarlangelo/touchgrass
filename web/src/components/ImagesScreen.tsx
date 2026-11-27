import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
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
import { Skeleton } from '@/components/ui/skeleton';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import ErrorState from './ErrorState.tsx';
import StoragePanel from './StoragePanel';
import HeartbeatStatus from './HeartbeatStatus.tsx';
import ScreenErrorBoundary from './ScreenErrorBoundary.tsx';
import { snapshotHeartbeat } from '@/lib/heartbeat.ts';
import { LatestRequest } from '@/lib/latest-request.ts';
import {
  fetchImages,
  isUnauthorized,
  pruneImages,
  type ImageView,
  type ServiceFreshness,
} from '@/lib/api.ts';
import { formatBytes, formatTime, shortId } from '@/lib/format.ts';

const BucketManager = lazy(() => import('./BucketManager.tsx'));

export default function ImagesScreen({
  onUnauthorized,
  isAdmin,
}: {
  onUnauthorized: () => void;
  isAdmin: boolean;
}) {
  const [bucketsOpen, setBucketsOpen] = useState(false);
  const [images, setImages] = useState<ImageView[] | null>(null);
  const [freshness, setFreshness] = useState<ServiceFreshness | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [pruning, setPruning] = useState(false);
  const [revision, setRevision] = useState(0);
  const request = useRef(new LatestRequest<void>()).current;

  const load = useCallback(() => request.run(async (signal) => {
    setError(null);

    try {
      const result = await fetchImages(signal);
      if (signal.aborted) return;
      setImages(result.images);
      setFreshness(result.freshness);
    } catch (err) {
      if (signal.aborted) return;
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }), [onUnauthorized, request]);

  useEffect(() => {
    const refresh = () => { if (!document.hidden) void load(); };
    refresh();
    const timer = window.setInterval(refresh, 30_000);
    document.addEventListener('visibilitychange', refresh);
    return () => {
      request.cancel();
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', refresh);
    };
  }, [load, request]);

  async function handlePrune(): Promise<void> {
    setPruning(true);

    try {
      const result = await pruneImages();
      toast.success(
        `Pruned ${result.deleted} image${result.deleted === 1 ? '' : 's'}, reclaimed ${formatBytes(result.reclaimed_bytes)}.`,
      );
      setConfirmOpen(false);
      setRevision(value => value + 1);
      request.cancel();
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      toast.error(err instanceof Error ? err.message : String(err));
    } finally {
      setPruning(false);
    }
  }

  const totalBytes = (images ?? []).reduce((sum, image) => sum + image.size_bytes, 0);
  const dangling = (images ?? []).filter((image) => image.dangling && image.containers === 0);
  const reclaimableBytes = dangling.reduce((sum, image) => sum + image.size_bytes, 0);
  const hasSample = freshness?.observed_at != null;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Storage management</h1>
        {isAdmin && (
          <Button
            variant="destructive"
            size="sm"
            className="ml-auto"
            disabled={images === null || dangling.length === 0}
            onClick={() => setConfirmOpen(true)}
          >
            Prune dangling ({dangling.length})
          </Button>
        )}
      </div>

      <StoragePanel revision={revision} onUnauthorized={onUnauthorized} />
      <h2 className="text-lg font-semibold">Docker images</h2>
      {freshness !== null && (
        <HeartbeatStatus
          label="Docker image heartbeat"
          state={snapshotHeartbeat(freshness)}
          observedAt={freshness.observed_at}
          detail={freshness.last_error_category === 'collection_failed'
            ? 'The Docker image list could not be collected. Check daemon access.'
            : freshness.stale ? 'The image collector is waiting for its next sample.' : undefined}
          action={freshness.stale ? (
            <Button size="sm" variant="outline" onClick={() => void load()}>Refresh images</Button>
          ) : null}
        />
      )}
      {error !== null && <ErrorState title="Couldn't load images" message={error} onRetry={() => void load()} />}
      <p className="text-muted-foreground text-sm">Image sizes include shared layers. Totals below are logical estimates, not physical disk usage or guaranteed reclaimable space.</p>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Images</CardTitle>
          </CardHeader>
          <CardContent>
            {images === null ? (
              <Skeleton className="h-8 w-16" />
            ) : (
              <p className="text-2xl font-semibold">{hasSample ? images.length : '—'}</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Total size</CardTitle>
          </CardHeader>
          <CardContent>
            {images === null ? (
              <Skeleton className="h-8 w-24" />
            ) : (
              <p className="text-2xl font-semibold">{hasSample ? formatBytes(totalBytes) : '—'}</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Dangling</CardTitle>
          </CardHeader>
          <CardContent>
            {images === null ? (
              <Skeleton className="h-8 w-16" />
            ) : (
              <p className="text-2xl font-semibold">{hasSample ? dangling.length : '—'}</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Dangling size estimate</CardTitle>
          </CardHeader>
          <CardContent>
            {images === null ? (
              <Skeleton className="h-8 w-24" />
            ) : (
              <p className="text-2xl font-semibold">{hasSample ? formatBytes(reclaimableBytes) : '—'}</p>
            )}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardContent className="pt-4">
          {images === null ? (
            <div className="space-y-2" aria-label="Loading images">
              <Skeleton className="h-10 w-full" />
              <Skeleton className="h-10 w-full" />
            </div>
          ) : images.length === 0 ? (
            <p className="text-muted-foreground py-6 text-center text-sm">
              {hasSample ? 'No images reported by the daemon.' : 'No image inventory has been collected yet.'}
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Repository:tag</TableHead>
                  <TableHead>ID</TableHead>
                  <TableHead className="text-right">Size</TableHead>
                  <TableHead className="text-right">Containers</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {images.map((image) => (
                  <TableRow key={image.id}>
                    <TableCell className="font-mono text-xs">
                      {image.repo_tags.length === 0 ? (
                        <span className="text-muted-foreground">&lt;none&gt;</span>
                      ) : (
                        image.repo_tags.join(', ')
                      )}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{shortId(image.id)}</TableCell>
                    <TableCell className="text-right font-mono">
                      {formatBytes(image.size_bytes)}
                    </TableCell>
                    <TableCell className="text-right font-mono">{image.containers}</TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {formatTime(image.created_at)}
                    </TableCell>
                    <TableCell>
                      {image.dangling ? (
                        <Badge variant="secondary">dangling</Badge>
                      ) : image.containers > 0 ? (
                        <Badge variant="default">in use</Badge>
                      ) : (
                        <Badge variant="outline">unused</Badge>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      {isAdmin && (
        <details className="rounded-lg border" onToggle={(event) => setBucketsOpen(event.currentTarget.open)}>
          <summary className="cursor-pointer p-4 text-sm font-medium">Browse object storage</summary>
          {bucketsOpen && (
            <ScreenErrorBoundary title="Couldn’t load bucket browsing">
              <Suspense fallback={<p className="p-4 text-sm" role="status">Loading buckets…</p>}>
                <BucketManager onUnauthorized={onUnauthorized} />
              </Suspense>
            </ScreenErrorBoundary>
          )}
        </details>
      )}

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Prune dangling images?</DialogTitle>
            <DialogDescription>
              Removes {dangling.length} dangling image{dangling.length === 1 ? '' : 's'} that no
              container references — {formatBytes(reclaimableBytes)} logical size. Actual reclaimed space may be lower because layers are shared. Tagged
              images are left alone. This can&apos;t be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => void handlePrune()} disabled={pruning}>
              {pruning ? 'Pruning…' : 'Prune images'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
