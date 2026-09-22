import { useState } from 'react';

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import LogSearch from './logs/LogSearch.tsx';
import type { ServiceView } from '@/lib/api.ts';

export default function LogsScreen({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState(services[0]?.id ?? '');

  const effectiveId = services.some((service) => service.id === serviceId)
    ? serviceId
    : (services[0]?.id ?? '');

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-semibold tracking-tight">Logs</h1>
        <Select value={effectiveId} onValueChange={setServiceId}>
          <SelectTrigger className="ml-auto w-48" aria-label="Service filter">
            <SelectValue placeholder="Pick a service" />
          </SelectTrigger>
          <SelectContent>
            {services.map((service) => (
              <SelectItem key={service.id} value={service.id}>
                {service.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {effectiveId === '' ? (
        <p className="text-muted-foreground text-sm">No services on the radar yet.</p>
      ) : (
        <LogSearch key={effectiveId} serviceId={effectiveId} onUnauthorized={onUnauthorized} />
      )}
    </div>
  );
}
