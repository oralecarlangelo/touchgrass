import { Checkbox } from '@/components/ui/checkbox';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import type { ServiceView, UserAccess } from '@/lib/api.ts';

// Roles an account can be given today. The developer role is reserved
// on the server and not offered until it exists.
export const assignableRoles = [
  { value: 'viewer', label: 'viewer (read-only)' },
  { value: 'admin', label: 'admin (full power, every service)' },
] as const;

export function defaultAccess(): UserAccess {
  return { role: 'viewer', all_services: true, services: [] };
}

// normalizeAccess is what gets sent: an admin always has every service,
// and an all-services scope carries no list.
export function normalizeAccess(access: UserAccess): UserAccess {
  if (access.role === 'admin' || access.all_services) {
    return { role: access.role, all_services: true, services: [] };
  }

  return { role: access.role, all_services: false, services: [...access.services].sort() };
}

// accessProblem returns why an access cannot be saved, or null.
export function accessProblem(access: UserAccess): string | null {
  const normalized = normalizeAccess(access);

  if (!normalized.all_services && normalized.services.length === 0) {
    return 'Pick at least one service, or choose all services.';
  }

  return null;
}

// AccessFields edits a role and a scope together: all services, or a
// checklist. It is shared by the new-user and the edit-access dialogs.
export default function AccessFields({
  idPrefix,
  access,
  onChange,
  services,
}: {
  idPrefix: string;
  access: UserAccess;
  onChange: (next: UserAccess) => void;
  services: ServiceView[];
}) {
  const isAdmin = access.role === 'admin';
  const allServices = isAdmin || access.all_services;

  function toggleService(id: string, checked: boolean): void {
    const next = checked
      ? [...new Set([...access.services, id])]
      : access.services.filter((service) => service !== id);

    onChange({ ...access, services: next });
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Label htmlFor={`${idPrefix}-role`}>Role</Label>
        <Select value={access.role} onValueChange={(role) => onChange({ ...access, role })}>
          <SelectTrigger id={`${idPrefix}-role`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {assignableRoles.map((role) => (
              <SelectItem key={role.value} value={role.value}>
                {role.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Services</legend>
        {isAdmin ? (
          <p className="text-muted-foreground text-sm">
            An admin always has every service and the host screens.
          </p>
        ) : (
          <>
            <label className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name={`${idPrefix}-scope`}
                className="mt-1"
                checked={allServices}
                onChange={() => onChange({ ...access, all_services: true })}
              />
              <span>
                All services
                <span className="text-muted-foreground block text-xs">
                  Includes services added later and the host screens: Fleet, Security, Databases,
                  Images, API keys and the full audit trail.
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2 text-sm">
              <input
                type="radio"
                name={`${idPrefix}-scope`}
                className="mt-1"
                checked={!allServices}
                onChange={() => onChange({ ...access, all_services: false })}
              />
              <span>
                Selected services
                <span className="text-muted-foreground block text-xs">
                  Only these services: their logs, issues, metrics, deploy history, env keys and
                  audit rows. No host screens.
                </span>
              </span>
            </label>
          </>
        )}

        {!allServices && (
          <div
            className="border-border max-h-48 space-y-2 overflow-y-auto rounded-md border p-3"
            role="group"
            aria-label="Services this user can see"
          >
            {services.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No services are managed yet. Add one first, then come back to grant it.
              </p>
            ) : (
              services.map((service) => (
                <div key={service.id} className="flex items-center gap-2">
                  <Checkbox
                    id={`${idPrefix}-service-${service.id}`}
                    checked={access.services.includes(service.id)}
                    onCheckedChange={(checked) => toggleService(service.id, checked === true)}
                  />
                  <Label htmlFor={`${idPrefix}-service-${service.id}`} className="font-normal">
                    {service.name}
                    {service.name !== service.id && (
                      <span className="text-muted-foreground ml-1 font-mono text-xs">{service.id}</span>
                    )}
                  </Label>
                </div>
              ))
            )}
          </div>
        )}
      </fieldset>
    </div>
  );
}
