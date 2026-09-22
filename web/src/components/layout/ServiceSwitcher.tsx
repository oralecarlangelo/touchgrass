import { Check, ChevronsUpDown, Server } from 'lucide-react';

import { Button } from '@/components/ui/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { healthDotClass } from '@/lib/views.ts';
import type { ServiceView } from '@/lib/api.ts';

export default function ServiceSwitcher({
  services,
  selectedId,
  onSelect,
}: {
  services: ServiceView[];
  selectedId: string | null;
  onSelect: (id: string | null) => void;
}) {
  const selected = services.find((service) => service.id === selectedId) ?? null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" aria-label="Switch service">
          <Server className="h-4 w-4" aria-hidden />
          <span className="max-w-32 truncate">{selected === null ? 'All services' : selected.name}</span>
          <ChevronsUpDown className="h-4 w-4 opacity-50" aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuLabel>Services</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => onSelect(null)}>
          <span className="flex-1">All services</span>
          {selected === null && <Check className="h-4 w-4" aria-hidden />}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {services.map((service) => (
          <DropdownMenuItem key={service.id} onSelect={() => onSelect(service.id)}>
            <span
              className={`h-2 w-2 shrink-0 rounded-full ${healthDotClass(service.health)}`}
              aria-hidden
            />
            <span className="flex-1 truncate">{service.name}</span>
            <span className="text-muted-foreground text-xs">{service.health}</span>
            {service.id === selectedId && <Check className="h-4 w-4" aria-hidden />}
          </DropdownMenuItem>
        ))}
        {services.length === 0 && (
          <DropdownMenuItem disabled>No services on the radar</DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
