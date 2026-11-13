import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import type { MigrationConfigInput, MigrationConfigResponse } from '@/lib/api.ts';
import { parseCommand } from '@/lib/migrations.ts';

export interface SetupForm {
  tool: string;
  databaseUrl: string;
  container: string;
  workdir: string;
  migrationsDir: string;
  command: string;
}

export function formFromConfig(setup: MigrationConfigResponse): SetupForm {
  const config = setup.config;

  return {
    tool: config?.tool ?? setup.tools[0]?.name ?? '',
    // The stored URL is never sent back, so the field starts empty.
    databaseUrl: '',
    container: config?.container ?? '',
    workdir: config?.workdir ?? '',
    migrationsDir: config?.migrations_dir ?? '',
    command: (config?.command ?? []).join(' '),
  };
}

export function inputFromForm(form: SetupForm): MigrationConfigInput {
  return {
    tool: form.tool,
    database_url: form.databaseUrl.trim(),
    container: form.container.trim(),
    workdir: form.workdir.trim(),
    migrations_dir: form.migrationsDir.trim(),
    command: parseCommand(form.command),
  };
}

function Field({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      <p id={`${id}-hint`} className="text-muted-foreground text-xs">
        {hint}
      </p>
    </div>
  );
}

// SetupFields is the portal-free body of the setup dialog.
export function SetupFields({
  setup,
  form,
  busy,
  onChange,
}: {
  setup: MigrationConfigResponse;
  form: SetupForm;
  busy: boolean;
  onChange: (form: SetupForm) => void;
}) {
  const stored = setup.config;
  // The hints quote the chosen tool's own defaults, so nothing here
  // names a particular tool.
  const chosen = setup.tools.find((tool) => tool.name === form.tool);
  const defaultCommand = chosen === undefined ? '' : chosen.default_command.join(' ');

  return (
    <div className="space-y-4">
      <Field id="migration-tool" label="Migration tool" hint="The tool the application migrates its database with.">
        <Select value={form.tool} disabled={busy} onValueChange={(tool) => onChange({ ...form, tool })}>
          <SelectTrigger id="migration-tool">
            <SelectValue placeholder="Choose a tool" />
          </SelectTrigger>
          <SelectContent>
            {setup.tools.map((tool) => (
              <SelectItem key={tool.name} value={tool.name}>
                {tool.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field
        id="migration-url"
        label="Database URL"
        hint={
          stored === null
            ? 'Used only to read the migration history, so a read-only database role is enough. It must be reachable from the touchgrass host. Stored encrypted and never shown again.'
            : `Stored: ${stored.database}. Leave blank to keep it. It is never shown again.`
        }
      >
        <Input
          id="migration-url"
          type="password"
          autoComplete="off"
          value={form.databaseUrl}
          disabled={busy}
          placeholder={stored === null ? 'The connection URL for this tool' : 'Leave blank to keep the stored URL'}
          onChange={(event) => onChange({ ...form, databaseUrl: event.target.value })}
        />
      </Field>
      <Field
        id="migration-container"
        label="Compose service (optional)"
        hint="The compose service whose container ships the migrations and runs the commands. Blank uses the service's own container (the live color for blue-green)."
      >
        <Input
          id="migration-container"
          value={form.container}
          disabled={busy}
          onChange={(event) => onChange({ ...form, container: event.target.value })}
        />
      </Field>
      <Field
        id="migration-workdir"
        label="Working directory (optional)"
        hint="Absolute path inside the container. Blank uses the container's working directory."
      >
        <Input
          id="migration-workdir"
          value={form.workdir}
          disabled={busy}
          placeholder="/app"
          onChange={(event) => onChange({ ...form, workdir: event.target.value })}
        />
      </Field>
      <Field
        id="migration-dir"
        label="Migrations directory (optional)"
        hint={`Relative to the working directory. Blank uses ${chosen === undefined ? "the tool's default" : chosen.default_migrations_dir}.`}
      >
        <Input
          id="migration-dir"
          value={form.migrationsDir}
          disabled={busy}
          onChange={(event) => onChange({ ...form, migrationsDir: event.target.value })}
        />
      </Field>
      <Field
        id="migration-command"
        label="Command (optional)"
        hint={`How the tool is started in the container, split on spaces with no shell. Blank uses ${defaultCommand === '' ? "the tool's default" : defaultCommand}.`}
      >
        <Input
          id="migration-command"
          value={form.command}
          disabled={busy}
          placeholder={defaultCommand}
          onChange={(event) => onChange({ ...form, command: event.target.value })}
        />
      </Field>
    </div>
  );
}

export default function MigrationSetupDialog({
  open,
  setup,
  serviceName,
  busy,
  error,
  onClose,
  onSave,
  onRemove,
}: {
  open: boolean;
  setup: MigrationConfigResponse | null;
  serviceName: string;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onSave: (input: MigrationConfigInput) => void;
  onRemove: () => void;
}) {
  const [form, setForm] = useState<SetupForm | null>(null);

  useEffect(() => {
    setForm(setup === null ? null : formFromConfig(setup));
  }, [setup, open]);

  const needsUrl = setup?.config === null || (setup?.config !== undefined && setup.config?.tool !== form?.tool);
  const ready = form !== null && form.tool !== '' && !busy && (!needsUrl || form.databaseUrl.trim() !== '');

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next && !busy) onClose(); }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>Migration setup for {serviceName}</DialogTitle>
          <DialogDescription>
            Tells touchgrass which database holds the migration history and which container ships the migrations.
          </DialogDescription>
        </DialogHeader>
        {setup === null || form === null ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (
          <SetupFields setup={setup} form={form} busy={busy} onChange={setForm} />
        )}
        {error !== null && (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        )}
        <DialogFooter className="sm:justify-between">
          <div>
            {setup?.config !== null && setup?.config !== undefined && (
              <Button type="button" variant="ghost" disabled={busy} onClick={onRemove}>
                Remove setup
              </Button>
            )}
          </div>
          <div className="flex gap-2">
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="button" disabled={!ready} onClick={() => form !== null && onSave(inputFromForm(form))}>
              {busy ? 'Saving…' : 'Save setup'}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
