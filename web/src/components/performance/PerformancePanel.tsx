import type { ReactNode } from 'react';

import ErrorState from '../ErrorState.tsx';

export function PanelError({ name, error, retry }: { name: string; error: string | null; retry: () => void }) {
  return error === null ? null : (
    <ErrorState title={`Couldn’t refresh ${name}`} message={error} onRetry={retry} />
  );
}

export function DetailsPanel({ title, description, onChange, children }: {
  title: string;
  description: string;
  onChange: (open: boolean) => void;
  children: ReactNode;
}) {
  return (
    <details className="bg-card rounded-lg border" onToggle={(event) => onChange(event.currentTarget.open)}>
      <summary className="cursor-pointer p-4 text-sm font-medium">
        {title}
        <span className="text-muted-foreground ml-2 font-normal">{description}</span>
      </summary>
      <div className="space-y-3 px-4 pb-4">{children}</div>
    </details>
  );
}
