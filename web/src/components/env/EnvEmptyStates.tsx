import { FileKey2, KeyRound, Settings2 } from 'lucide-react';

import { Button } from '@/components/ui/button';
import type { EnvImportResult, EnvNotConfiguredReason, EnvTarget } from '@/lib/api.ts';
import { envDocsHref, envKeyEnvVar, envTargetConfigSnippet } from '@/lib/env.ts';

function Shell({
  icon,
  title,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="border-border bg-card rounded-lg border px-6 py-10">
      <div className="mx-auto max-w-xl space-y-3 text-center">
        <div className="text-muted-foreground mx-auto flex size-9 items-center justify-center rounded-full border">
          {icon}
        </div>
        <p className="text-sm font-medium">{title}</p>
        {children}
      </div>
    </div>
  );
}

export function EnvNotConfigured({
  reason,
  serviceId,
  isAdmin,
}: {
  reason: EnvNotConfiguredReason | undefined;
  serviceId: string;
  isAdmin: boolean;
}) {
  if (reason === 'key_missing') {
    return (
      <Shell icon={<KeyRound className="size-4" aria-hidden />} title="This host has no encryption key">
        <p className="text-muted-foreground text-sm">
          Secrets are encrypted at rest, so touchgrass needs a key before it can store or render any
          environment entry. {isAdmin ? 'Set one on the host and restart the server.' : 'Ask an admin to set one on the host.'}
        </p>
        {isAdmin && (
          <pre className="rounded-md bg-zinc-950 p-3 text-left font-mono text-xs leading-relaxed text-zinc-100 dark:bg-zinc-900">
            {`# generate once, keep it out of version control\n${envKeyEnvVar}=$(openssl rand -base64 32)`}
          </pre>
        )}
        <p className="text-muted-foreground text-xs">
          The key never leaves the host.{' '}
          <a href={envDocsHref} target="_blank" rel="noreferrer" className="text-primary underline-offset-4 hover:underline">
            Environment guide
          </a>
        </p>
      </Shell>
    );
  }

  return (
    <Shell icon={<Settings2 className="size-4" aria-hidden />} title="Configure the env target">
      <p className="text-muted-foreground text-sm">
        touchgrass does not know which file on the host holds this service's environment yet.
        {isAdmin
          ? ' Add the target to the service config, then reload this tab.'
          : ' An admin adds the target to the service config.'}
      </p>
      {isAdmin && (
        <pre
          aria-label="Service config snippet"
          className="rounded-md bg-zinc-950 p-3 text-left font-mono text-xs leading-relaxed text-zinc-100 dark:bg-zinc-900"
        >
          {envTargetConfigSnippet(serviceId)}
        </pre>
      )}
      <p className="text-muted-foreground text-xs">
        The path is written atomically with that owner and mode; the previous file is kept for rollback.{' '}
        <a href={envDocsHref} target="_blank" rel="noreferrer" className="text-primary underline-offset-4 hover:underline">
          Environment guide
        </a>
      </p>
    </Shell>
  );
}

export function EnvNoEntries({
  target,
  isAdmin,
  busy,
  error,
  result,
  onImport,
}: {
  target: EnvTarget | null;
  isAdmin: boolean;
  busy: boolean;
  error: string | null;
  result: EnvImportResult | null;
  onImport: () => void;
}) {
  return (
    <Shell icon={<FileKey2 className="size-4" aria-hidden />} title="No entries yet">
      <p className="text-muted-foreground text-sm">
        {target !== null ? (
          <>
            The target is <span className="text-foreground font-mono text-xs">{target.path}</span>.{' '}
          </>
        ) : null}
        {isAdmin
          ? 'Import the file that is on the host now so every key is tracked, or add entries one by one.'
          : 'An admin imports the current file or adds entries; they appear here once staged.'}
      </p>
      {isAdmin && (
        <div className="flex justify-center">
          <Button type="button" disabled={busy} onClick={onImport} aria-label="Import the current file">
            {busy ? 'Importing…' : 'Import the current file'}
          </Button>
        </div>
      )}
      {result !== null && (
        <p className="text-sm" role="status">
          Imported {result.imported} {result.imported === 1 ? 'entry' : 'entries'}
          {result.skipped > 0 ? `, skipped ${result.skipped}` : ''}
          {result.secret_guessed > 0 ? `; ${result.secret_guessed} marked secret by name` : ''}.
        </p>
      )}
      {error !== null && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}
    </Shell>
  );
}
