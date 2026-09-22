import { useEffect, useState } from 'react';
import { toast } from 'sonner';

import { Badge } from '@/components/ui/badge';
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Skeleton } from '@/components/ui/skeleton';
import {
  createService,
  isUnauthorized,
  suggestService,
  type OnboardingStrategy,
  type OnboardingSuggest,
} from '@/lib/api.ts';

interface RecreateFields {
  service: string;
  health_url: string;
  deploy_script: string;
  rollback_script: string;
}

interface BlueGreenFields {
  blue_service: string;
  green_service: string;
  blue_target: string;
  green_target: string;
  blue_url: string;
  green_url: string;
  nginx_conf: string;
  marker: string;
  cutover_script: string;
}

function Field({
  id,
  label,
  value,
  onChange,
  placeholder,
  note,
}: {
  id: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  note?: string;
}) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        className="font-mono text-xs"
      />
      {note !== undefined && note !== '' && (
        <p className="text-muted-foreground text-xs">{note}</p>
      )}
    </div>
  );
}

function confidenceVariant(confidence: string): 'default' | 'secondary' | 'outline' {
  switch (confidence) {
    case 'high':
      return 'default';
    case 'medium':
      return 'secondary';
    default:
      return 'outline';
  }
}

export default function PromoteDialog({
  container,
  onClose,
  onCreated,
  onUnauthorized,
}: {
  container: string | null;
  onClose: () => void;
  onCreated: (id: string) => void;
  onUnauthorized: () => void;
}) {
  const [draft, setDraft] = useState<OnboardingSuggest | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [serviceId, setServiceId] = useState('');
  const [strategy, setStrategy] = useState<OnboardingStrategy>('recreate');
  const [composeProject, setComposeProject] = useState('');
  const [composeDir, setComposeDir] = useState('');
  const [publicUrl, setPublicUrl] = useState('');
  const [recreate, setRecreate] = useState<RecreateFields>({
    service: '',
    health_url: '',
    deploy_script: '',
    rollback_script: '',
  });
  const [bluegreen, setBluegreen] = useState<BlueGreenFields>({
    blue_service: '',
    green_service: '',
    blue_target: '',
    green_target: '',
    blue_url: '',
    green_url: '',
    nginx_conf: '',
    marker: '',
    cutover_script: '',
  });

  const [submitError, setSubmitError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    if (container === null) {
      return;
    }

    let cancelled = false;
    setDraft(null);
    setLoading(true);
    setLoadError(null);
    setSubmitError(null);

    void suggestService(container)
      .then((suggestion) => {
        if (cancelled) {
          return;
        }

        setDraft(suggestion);
        setServiceId(suggestion.service_id);
        setStrategy(suggestion.strategy === 'bluegreen' ? 'bluegreen' : 'recreate');
        setComposeProject(suggestion.compose_project);
        setComposeDir(suggestion.compose_dir);
        setPublicUrl(suggestion.public_url);
        setRecreate({
          service: suggestion.service,
          health_url: suggestion.health_url,
          deploy_script: suggestion.deploy_script,
          rollback_script: suggestion.rollback_script,
        });
        setBluegreen({
          blue_service: suggestion.blue_service ?? '',
          green_service: suggestion.green_service ?? '',
          blue_target: suggestion.blue_target ?? '',
          green_target: suggestion.green_target ?? '',
          blue_url: suggestion.blue_url ?? '',
          green_url: suggestion.green_url ?? '',
          nginx_conf: suggestion.nginx_conf ?? '',
          marker: suggestion.marker ?? '',
          cutover_script: suggestion.cutover_script ?? '',
        });
      })
      .catch((err: unknown) => {
        if (cancelled) {
          return;
        }

        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setLoadError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [container, onUnauthorized]);

  function setRecreateField(field: keyof RecreateFields, value: string): void {
    setRecreate((current) => ({ ...current, [field]: value }));
  }

  function setBlueGreenField(field: keyof BlueGreenFields, value: string): void {
    setBluegreen((current) => ({ ...current, [field]: value }));
  }

  async function handleCreate(): Promise<void> {
    if (creating) {
      return;
    }

    const trimmedId = serviceId.trim();

    if (trimmedId === '') {
      setSubmitError('Service id is required.');
      return;
    }

    if (!/^[a-z0-9-]+$/.test(trimmedId)) {
      setSubmitError('Service id must match ^[a-z0-9-]+$.');
      return;
    }

    if (composeProject.trim() === '' || composeDir.trim() === '') {
      setSubmitError('Compose project and compose dir are required.');
      return;
    }

    if (strategy === 'recreate') {
      if (
        recreate.service.trim() === '' ||
        recreate.health_url.trim() === '' ||
        recreate.deploy_script.trim() === '' ||
        recreate.rollback_script.trim() === ''
      ) {
        setSubmitError('Service, health URL, and both script paths are required for recreate.');
        return;
      }
    } else if (
      bluegreen.blue_service.trim() === '' ||
      bluegreen.green_service.trim() === '' ||
      bluegreen.blue_target.trim() === '' ||
      bluegreen.green_target.trim() === '' ||
      bluegreen.blue_url.trim() === '' ||
      bluegreen.green_url.trim() === '' ||
      bluegreen.nginx_conf.trim() === '' ||
      bluegreen.marker.trim() === '' ||
      bluegreen.cutover_script.trim() === ''
    ) {
      setSubmitError('All blue-green fields are required for the bluegreen strategy.');
      return;
    }

    setCreating(true);
    setSubmitError(null);

    try {
      const created = await createService({
        id: trimmedId,
        strategy,
        compose_project: composeProject.trim(),
        compose_dir: composeDir.trim(),
        config:
          strategy === 'recreate'
            ? {
                service: recreate.service.trim(),
                health_url: recreate.health_url.trim(),
                public_url: publicUrl.trim(),
                deploy_script: recreate.deploy_script.trim(),
                rollback_script: recreate.rollback_script.trim(),
              }
            : {
                blue_service: bluegreen.blue_service.trim(),
                green_service: bluegreen.green_service.trim(),
                blue_target: bluegreen.blue_target.trim(),
                green_target: bluegreen.green_target.trim(),
                blue_url: bluegreen.blue_url.trim(),
                green_url: bluegreen.green_url.trim(),
                nginx_conf: bluegreen.nginx_conf.trim(),
                marker: bluegreen.marker.trim(),
                cutover_script: bluegreen.cutover_script.trim(),
                public_url: publicUrl.trim(),
              },
      });

      toast.success(`Service ${created.id} created — now managed.`);
      onCreated(created.id);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setSubmitError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  }

  const probeNote =
    draft === null
      ? ''
      : draft.health_url === ''
        ? 'Probe found no responding port — fill in manually.'
        : 'Prefilled from a successful container-port probe — verify before creating.';

  return (
    <Dialog open={container !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Manage {container ?? ''}</DialogTitle>
          <DialogDescription>
            Promote this observed container to a managed service. Review the draft, fix the
            blanks, then create the service row. Nothing deploys.
          </DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="space-y-2" aria-label="Loading suggestion">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        )}

        {!loading && loadError !== null && (
          <div className="space-y-3">
            <p className="text-destructive text-sm" role="alert">
              Couldn&apos;t draft the service: {loadError}
            </p>
            <DialogFooter>
              <Button variant="outline" onClick={onClose}>
                Close
              </Button>
            </DialogFooter>
          </div>
        )}

        {!loading && loadError === null && draft !== null && (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={confidenceVariant(draft.confidence)}>
                confidence: {draft.confidence === '' ? 'unknown' : draft.confidence}
              </Badge>
              <span className="text-muted-foreground text-xs">
                container <span className="font-mono">{draft.container}</span>
              </span>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                id="promote-id"
                label="Service id"
                value={serviceId}
                onChange={setServiceId}
                placeholder="my-service"
              />
              <div className="space-y-1">
                <Label htmlFor="promote-strategy">Strategy</Label>
                <Select
                  value={strategy}
                  onValueChange={(value) =>
                    setStrategy(value === 'bluegreen' ? 'bluegreen' : 'recreate')
                  }
                >
                  <SelectTrigger id="promote-strategy" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="recreate">recreate</SelectItem>
                    <SelectItem value="bluegreen">bluegreen</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <Field
                id="promote-project"
                label="Compose project"
                value={composeProject}
                onChange={setComposeProject}
              />
              <Field
                id="promote-dir"
                label="Compose dir"
                value={composeDir}
                onChange={setComposeDir}
              />
            </div>

            {strategy === 'recreate' ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  id="promote-service"
                  label="Service"
                  value={recreate.service}
                  onChange={(value) => setRecreateField('service', value)}
                />
                <Field
                  id="promote-health"
                  label="Health URL"
                  value={recreate.health_url}
                  onChange={(value) => setRecreateField('health_url', value)}
                  placeholder="http://127.0.0.1:8080/health"
                  note={probeNote}
                />
                <Field
                  id="promote-public"
                  label="Public URL"
                  value={publicUrl}
                  onChange={setPublicUrl}
                  placeholder="https://example.com/health"
                />
                <Field
                  id="promote-deploy-script"
                  label="Deploy script"
                  value={recreate.deploy_script}
                  onChange={(value) => setRecreateField('deploy_script', value)}
                />
                <div className="sm:col-span-2">
                  <Field
                    id="promote-rollback-script"
                    label="Rollback script"
                    value={recreate.rollback_script}
                    onChange={(value) => setRecreateField('rollback_script', value)}
                  />
                </div>
              </div>
            ) : (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field
                  id="promote-blue-service"
                  label="Blue service"
                  value={bluegreen.blue_service}
                  onChange={(value) => setBlueGreenField('blue_service', value)}
                />
                <Field
                  id="promote-green-service"
                  label="Green service"
                  value={bluegreen.green_service}
                  onChange={(value) => setBlueGreenField('green_service', value)}
                />
                <Field
                  id="promote-blue-target"
                  label="Blue target"
                  value={bluegreen.blue_target}
                  onChange={(value) => setBlueGreenField('blue_target', value)}
                />
                <Field
                  id="promote-green-target"
                  label="Green target"
                  value={bluegreen.green_target}
                  onChange={(value) => setBlueGreenField('green_target', value)}
                />
                <Field
                  id="promote-blue-url"
                  label="Blue URL"
                  value={bluegreen.blue_url}
                  onChange={(value) => setBlueGreenField('blue_url', value)}
                  note={probeNote}
                />
                <Field
                  id="promote-green-url"
                  label="Green URL"
                  value={bluegreen.green_url}
                  onChange={(value) => setBlueGreenField('green_url', value)}
                />
                <Field
                  id="promote-public-bg"
                  label="Public URL"
                  value={publicUrl}
                  onChange={setPublicUrl}
                  placeholder="https://example.com/health"
                />
                <Field
                  id="promote-nginx"
                  label="Nginx conf"
                  value={bluegreen.nginx_conf}
                  onChange={(value) => setBlueGreenField('nginx_conf', value)}
                />
                <Field
                  id="promote-marker"
                  label="Marker"
                  value={bluegreen.marker}
                  onChange={(value) => setBlueGreenField('marker', value)}
                />
                <Field
                  id="promote-cutover"
                  label="Cutover script"
                  value={bluegreen.cutover_script}
                  onChange={(value) => setBlueGreenField('cutover_script', value)}
                />
              </div>
            )}

            {draft.reasons.length > 0 && (
              <div className="space-y-1">
                <p className="text-sm font-medium">Why this draft</p>
                <ul className="text-muted-foreground list-disc space-y-0.5 pl-5 text-xs">
                  {draft.reasons.map((reason) => (
                    <li key={reason}>{reason}</li>
                  ))}
                </ul>
              </div>
            )}

            {draft.warnings.length > 0 && (
              <div className="space-y-1">
                <p className="text-sm font-medium">Warnings</p>
                <ul className="list-disc space-y-0.5 pl-5 text-xs text-amber-600 dark:text-amber-400">
                  {draft.warnings.map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              </div>
            )}

            {submitError !== null && (
              <p className="text-destructive text-sm" role="alert">
                {submitError}
              </p>
            )}

            <DialogFooter>
              <Button variant="outline" onClick={onClose} disabled={creating}>
                Cancel
              </Button>
              <Button onClick={() => void handleCreate()} disabled={creating}>
                {creating ? 'Creating…' : 'Create service'}
              </Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
