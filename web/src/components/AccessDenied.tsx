import { Lock } from 'lucide-react';

import { Button } from '@/components/ui/button';

// AccessDenied is the one state shown when the signed-in user may not
// open what they asked for: a screen, or a service outside their scope.
// It says what is missing and offers a way back to something they can
// use.
export default function AccessDenied({
  what,
  detail,
  backLabel,
  onBack,
}: {
  what: string;
  detail: string;
  backLabel: string;
  onBack: () => void;
}) {
  return (
    <div
      role="alert"
      className="border-border bg-card flex flex-col items-center rounded-lg border px-6 py-12 text-center"
    >
      <Lock className="text-muted-foreground h-5 w-5" aria-hidden />
      <p className="mt-2 text-sm font-medium">You don&apos;t have access to {what}</p>
      <p className="text-muted-foreground mt-1 max-w-sm text-sm">{detail}</p>
      <Button variant="outline" size="sm" className="mt-4" onClick={onBack}>
        {backLabel}
      </Button>
    </div>
  );
}
