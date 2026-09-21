import { useCallback, useEffect, useState } from 'react';
import { fetchAudit, isUnauthorized, type AuditEntry, type ServiceView } from '../lib/api.ts';

function formatTimestamp(value: string): string {
  const parsed = new Date(value);

  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return parsed.toLocaleString();
}

export default function AuditLog({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState('');
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);

    try {
      setEntries(await fetchAudit(serviceId));
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [serviceId, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="space-y-4">
      <section aria-label="audit log" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900">Audit log</h2>
          <label className="ml-auto text-sm text-gray-700">
            Service{' '}
            <select
              value={serviceId}
              onChange={(event) => setServiceId(event.target.value)}
              className="rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-sm text-gray-900"
            >
              <option value="">all services</option>
              {services.map((service) => (
                <option key={service.id} value={service.id}>
                  {service.name}
                </option>
              ))}
            </select>
          </label>
          <button
            type="button"
            onClick={() => void load()}
            disabled={loading}
            aria-label="Refresh audit log"
            className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
          >
            {loading ? 'Checking…' : 'Refresh'}
          </button>
        </div>

        <p className="mt-1 text-sm text-gray-600">Entries are append-only.</p>

        {error !== null && (
          <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
            <p className="text-sm text-red-700">{error}</p>
          </div>
        )}

        {entries === null ? (
          <p className="mt-3 text-sm text-gray-500">Loading entries…</p>
        ) : entries.length === 0 ? (
          <p className="mt-3 text-sm text-gray-500">No audit entries yet.</p>
        ) : (
          <ul className="mt-3 divide-y divide-gray-100">
            {entries.map((entry) => (
              <li key={entry.id} className="py-2 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-gray-900">{formatTimestamp(entry.created_at)}</span>
                  <span className="text-gray-700">{entry.actor}</span>
                  <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700">
                    {entry.action}
                  </span>
                  <span
                    className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      entry.result === 'success'
                        ? 'bg-green-100 text-green-800'
                        : 'bg-red-100 text-red-800'
                    }`}
                  >
                    {entry.result}
                  </span>
                  <span className="text-xs text-gray-500">
                    {entry.service_id === null ? 'global' : entry.service_id}
                  </span>
                </div>
                {entry.detail !== '' && <p className="mt-0.5 text-sm text-gray-500">{entry.detail}</p>}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
