import { useEffect, useState } from 'react';

import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import type { SecurityPolicy, SecurityPolicyBody } from '@/lib/api.ts';

const toggles: { key: keyof SecurityPolicyBody; label: string }[] = [
  { key: 'allow_privileged', label: 'Allow privileged mode' },
  { key: 'allow_host_pid', label: 'Allow host PID namespace' },
  { key: 'allow_host_network', label: 'Allow host network' },
  { key: 'allow_docker_socket', label: 'Allow Docker socket' },
  { key: 'allow_root', label: 'Allow root user' },
  { key: 'allow_writable_root_fs', label: 'Allow writable root filesystem' },
];

const lists: { key: keyof SecurityPolicyBody; label: string; placeholder: string }[] = [
  { key: 'allowed_capabilities', label: 'Allowed capabilities', placeholder: 'NET_BIND_SERVICE' },
  { key: 'allowed_published_ports', label: 'Allowed published ports', placeholder: '127.0.0.1:8080->80/tcp' },
  { key: 'allowed_executable_paths', label: 'Allowed executable paths', placeholder: '/tmp/health-check' },
  { key: 'allowed_writable_paths', label: 'Allowed writable paths', placeholder: '/var/cache/app' },
];

export default function PolicyDialog({
  open,
  policy,
  busy,
  error,
  onClose,
  onSubmit,
}: {
  open: boolean;
  policy: SecurityPolicy | null;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onSubmit: (scope: SecurityPolicy['scope'], scopeId: string, body: SecurityPolicyBody) => void;
}) {
  const [scope, setScope] = useState<SecurityPolicy['scope']>('global');
  const [scopeId, setScopeId] = useState('');
  const [draft, setDraft] = useState<SecurityPolicyBody>({});

  useEffect(() => {
    setScope(policy?.scope ?? 'global');
    setScopeId(policy?.scope_id ?? '');
    setDraft(policy ?? {});
  }, [policy, open]);

  const valid = scope === 'global' || scopeId.trim() !== '';

  return (
    <Dialog open={open} onOpenChange={(next) => { if (!next) onClose(); }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{policy === null ? 'New security policy' : 'Edit security policy'}</DialogTitle>
          <DialogDescription>Scope exceptions by exact service or durable container ID. Avoid tenant-specific defaults.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label>Scope</Label>
              <Select value={scope} disabled={policy !== null} onValueChange={(value) => setScope(value as SecurityPolicy['scope'])}>
                <SelectTrigger aria-label="Policy scope"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="global">Global</SelectItem>
                  <SelectItem value="service">Service</SelectItem>
                  <SelectItem value="container">Container</SelectItem>
                </SelectContent>
              </Select>
            </div>
            {scope !== 'global' && (
              <div className="space-y-1.5">
                <Label htmlFor="policy-scope-id">Scope identifier</Label>
                <Input id="policy-scope-id" value={scopeId} disabled={policy !== null} onChange={(event) => setScopeId(event.target.value)} placeholder={scope === 'service' ? 'service-id' : 'durable-container-id'} />
              </div>
            )}
          </div>
          <div className="grid gap-2 sm:grid-cols-2">
            {toggles.map((item) => (
              <label key={item.key} className="flex items-center gap-2 rounded-md border p-2 text-sm">
                <Checkbox checked={draft[item.key] === true} onCheckedChange={(checked) => setDraft((current) => ({ ...current, [item.key]: checked === true }))} />
                {item.label}
              </label>
            ))}
          </div>
          {lists.map((item) => (
            <div key={item.key} className="space-y-1.5">
              <Label htmlFor={`policy-${item.key}`}>{item.label}</Label>
              <Input
                id={`policy-${item.key}`}
                value={((draft[item.key] as string[] | undefined) ?? []).join(', ')}
                placeholder={item.placeholder}
                onChange={(event) => setDraft((current) => ({ ...current, [item.key]: event.target.value.split(',').map((value) => value.trim()).filter(Boolean) }))}
              />
              <p className="text-muted-foreground text-xs">Comma-separated; values are stored exactly as policy metadata.</p>
            </div>
          ))}
          {error !== null && <p role="alert" className="text-destructive text-sm">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button disabled={busy || !valid} onClick={() => onSubmit(scope, scope === 'global' ? '' : scopeId.trim(), draft)}>
            {busy ? 'Saving…' : 'Save policy'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
