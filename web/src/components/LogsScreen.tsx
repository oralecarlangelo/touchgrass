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
import { readInvestigation } from '@/lib/investigation.ts';
import { useLocationHash } from '@/lib/use-location-hash.ts';

export default function LogsScreen({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const hash = useLocationHash('logs');
  const [selection, setSelection] = useState<{ hash: string; serviceId: string } | null>(null);
  if (hash === null) return null;

  const filters = readInvestigation(hash);
  const serviceId = selection?.hash === hash ? selection.serviceId : filters.serviceId;
  const setServiceId = (next: string) => setSelection({ hash, serviceId: next });

  const effectiveId = serviceId === undefined
    ? (services[0]?.id ?? '')
    : services.find((service) => service.id === serviceId)?.id ?? '';

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
        <p className="text-muted-foreground text-sm">
          {serviceId === undefined ? 'No services on the radar yet.' : 'Select an available service to search its logs.'}
        </p>
      ) : (
        <LogSearch
          key={`${effectiveId}:${hash}`}
          serviceId={effectiveId}
          initialAfter={filters.start}
          initialBefore={filters.end}
          onUnauthorized={onUnauthorized}
        />
      )}
    </div>
  );
}
