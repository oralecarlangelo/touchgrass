import type { ServiceView } from '../lib/api.ts';

function healthDot(health: string): string {
  switch (health) {
    case 'healthy':
      return 'bg-green-500';
    case 'unhealthy':
      return 'bg-red-500';
    default:
      return 'bg-gray-400';
  }
}

function healthCaption(health: string): string {
  switch (health) {
    case 'healthy':
      return 'cooking';
    case 'unhealthy':
      return 'down bad';
    default:
      return 'no signal';
  }
}

export default function ServiceCard({
  service,
  onSelect,
}: {
  service: ServiceView;
  onSelect: (service: ServiceView) => void;
}) {
  return (
    <section
      aria-label={`service ${service.name}`}
      className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm"
    >
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-lg font-semibold text-gray-900">{service.name}</h2>
        <span className="rounded-full bg-gray-100 px-2.5 py-0.5 text-xs font-medium text-gray-700">
          {service.strategy}
        </span>
        {service.live_color !== '' && (
          <span className="rounded-full bg-blue-100 px-2.5 py-0.5 text-xs font-medium text-blue-800">
            live: {service.live_color}
          </span>
        )}
        <span className="ml-auto flex items-center gap-2 text-sm text-gray-700">
          <span
            aria-hidden="true"
            className={`inline-block h-2.5 w-2.5 rounded-full ${healthDot(service.health)}`}
          />
          {service.health} · {healthCaption(service.health)}
        </span>
        <button
          type="button"
          onClick={() => onSelect(service)}
          aria-label={`View ${service.name} details`}
          className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          Details
        </button>
      </div>

      {service.colors.length > 0 && (
        <div className="mt-4">
          <h3 className="text-sm font-medium text-gray-500">Colors</h3>
          <ul className="mt-1 divide-y divide-gray-100">
            {service.colors.map((color) => (
              <li key={color.name} className="flex flex-wrap items-center gap-2 py-1.5 text-sm">
                <span className="font-medium text-gray-900">{color.name}</span>
                <span className="font-mono text-xs text-gray-500">{color.target}</span>
                <span className="flex items-center gap-1.5 text-gray-700">
                  <span
                    aria-hidden="true"
                    className={`inline-block h-2 w-2 rounded-full ${healthDot(color.health)}`}
                  />
                  {color.health}
                </span>
                {color.live && (
                  <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs font-medium text-blue-800">
                    LIVE
                  </span>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}

      <div className="mt-4">
        <h3 className="text-sm font-medium text-gray-500">Containers</h3>
        {service.containers.length === 0 ? (
          <p className="mt-1 text-sm text-gray-500">No running containers matched.</p>
        ) : (
          <ul className="mt-1 divide-y divide-gray-100">
            {service.containers.map((container) => (
              <li key={container.id} className="py-1.5 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-gray-900">{container.name}</span>
                  <span className="font-mono text-xs text-gray-500">{container.sha}</span>
                  <span className="text-xs text-gray-500">{container.state}</span>
                </div>
                <div className="mt-0.5 font-mono text-xs text-gray-500">
                  {container.image} · {container.ports.join(', ') || 'no published ports'}
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </section>
  );
}
