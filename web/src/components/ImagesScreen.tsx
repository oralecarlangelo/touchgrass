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
import {
  fetchImages,
  isUnauthorized,
  pruneImages,
  type ImageView,
} from '@/lib/api.ts';
import { formatBytes, formatTime, shortId } from '@/lib/format.ts';

export default function ImagesScreen({ onUnauthorized }: { onUnauthorized: () => void }) {
  const [images, setImages] = useState<ImageView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [pruning, setPruning] = useState(false);

  const load = useCallback(async () => {
    setError(null);

    try {
      setImages(await fetchImages());
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    }
  }, [onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  async function handlePrune(): Promise<void> {
    setPruning(true);

    try {
      const result = await pruneImages();
      toast.success(
        `Pruned ${result.deleted} image${result.deleted === 1 ? '' : 's'}, reclaimed ${formatBytes(result.reclaimed_bytes)}.`,
      );
      setConfirmOpen(false);
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

  if (error !== null) {
    return <ErrorState title="Couldn't load images" message={error} onRetry={() => void load()} />;
  }

  const totalBytes = (images ?? []).reduce((sum, image) => sum + image.size_bytes, 0);
  const dangling = (images ?? []).filter((image) => image.dangling);
  const reclaimableBytes = dangling.reduce((sum, image) => sum + image.size_bytes, 0);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Docker images</h1>
        <Button
          variant="destructive"
          size="sm"
          className="ml-auto"
          disabled={images === null || dangling.length === 0}
          onClick={() => setConfirmOpen(true)}
        >
          Prune dangling ({dangling.length})
        </Button>
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Images</CardTitle>
          </CardHeader>
          <CardContent>
            {images === null ? (
              <Skeleton className="h-8 w-16" />
            ) : (
              <p className="text-2xl font-semibold">{images.length}</p>
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
              <p className="text-2xl font-semibold">{formatBytes(totalBytes)}</p>
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
              <p className="text-2xl font-semibold">{dangling.length}</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-medium">Reclaimable</CardTitle>
          </CardHeader>
          <CardContent>
            {images === null ? (
              <Skeleton className="h-8 w-24" />
            ) : (
              <p className="text-2xl font-semibold">{formatBytes(reclaimableBytes)}</p>
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
              No images reported by the daemon.
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

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Prune dangling images?</DialogTitle>
            <DialogDescription>
              Removes {dangling.length} dangling image{dangling.length === 1 ? '' : 's'} that no
              container references — about {formatBytes(reclaimableBytes)} reclaimable. Tagged
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
