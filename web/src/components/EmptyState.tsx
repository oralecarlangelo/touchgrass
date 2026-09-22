import type { ReactNode } from 'react';

export default function EmptyState({
  title,
  body,
  action,
}: {
  title: string;
  body?: string;
  action?: ReactNode;
}) {
  return (
    <div className="border-border bg-card flex flex-col items-center rounded-lg border px-6 py-12 text-center">
      <p className="text-sm font-medium">{title}</p>
      {body !== undefined && <p className="text-muted-foreground mt-1 max-w-sm text-sm">{body}</p>}
      {action !== undefined && <div className="mt-4">{action}</div>}
    </div>
  );
}
