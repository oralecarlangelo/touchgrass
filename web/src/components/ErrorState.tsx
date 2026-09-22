import { TriangleAlert } from 'lucide-react';

import { Button } from '@/components/ui/button';

export default function ErrorState({
  title,
  message,
  onRetry,
}: {
  title: string;
  message: string;
  onRetry: () => void;
}) {
  return (
    <div
      role="alert"
      className="border-destructive/40 bg-card flex flex-col items-center rounded-lg border px-6 py-12 text-center"
    >
      <TriangleAlert className="text-destructive h-5 w-5" aria-hidden />
      <p className="mt-2 text-sm font-medium">{title}</p>
      <p className="text-muted-foreground mt-1 max-w-sm text-sm">{message}</p>
      <Button variant="outline" size="sm" className="mt-4" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}
