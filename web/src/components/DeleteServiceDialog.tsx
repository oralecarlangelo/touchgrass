import { useEffect, useState } from 'react';
import { toast } from 'sonner';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { deleteService, type ServiceView } from '@/lib/api.ts';

export default function DeleteServiceDialog({
  service,
  onClose,
  onDeleted,
}: {
  service: ServiceView | null;
  onClose: () => void;
  onDeleted: (id: string) => void;
}) {
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setDeleting(false);
    setError(null);
  }, [service?.id]);

  async function handleConfirm() {
    if (service === null || deleting) {
      return;
    }

    setDeleting(true);
    setError(null);

    try {
      await deleteService(service.id);
      toast.success(`Removed ${service.id} from management`);
      onDeleted(service.id);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setDeleting(false);
    }
  }

  return (
    <Dialog
      open={service !== null}
      onOpenChange={(open) => {
        if (!open) {
          onClose();
        }
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove {service?.id ?? 'service'}?</DialogTitle>
          <DialogDescription>
            This stops managing the service and deletes its touchgrass history — deploy records,
            alert rules, API keys, metrics, logs, and issues.
          </DialogDescription>
        </DialogHeader>
        <p className="text-sm">
          Running containers are <span className="font-medium">not touched</span>: the workload
          keeps serving, and the container shows up as unmanaged on the Fleet page.
        </p>
        {error !== null && (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={deleting}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={() => void handleConfirm()} disabled={deleting}>
            {deleting ? 'Removing…' : 'Remove service'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
