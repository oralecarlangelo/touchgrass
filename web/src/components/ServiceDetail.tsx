import { useCallback, useEffect, useState } from 'react';
import CutoverPanel from './CutoverPanel.tsx';
import DeployPanel from './DeployPanel.tsx';
import RollbackPanel from './RollbackPanel.tsx';
import {
  createRule,
  deleteRule,
  fetchDeploys,
  fetchMetrics,
  fetchRules,
  recordDeploy,
  type AlertRule,
  type Deploy,
  type Metric,
  type ServiceView,
} from '../lib/api.ts';

function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }

  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatUptime(secs: number): string {
  if (secs < 60) {
    return `${secs}s`;
  }
  if (secs < 3600) {
    return `${Math.floor(secs / 60)}m`;
  }
  if (secs < 86400) {
    return `${Math.floor(secs / 3600)}h`;
  }

  return `${Math.floor(secs / 86400)}d`;
}

function latestByContainer(metrics: Metric[]): Metric[] {
  const latest = new Map<string, Metric>();

  for (const metric of metrics) {
    const current = latest.get(metric.container_name);
    if (current === undefined || metric.sampled_at > current.sampled_at) {
      latest.set(metric.container_name, metric);
    }
  }

  return [...latest.values()];
}

function memPercent(metric: Metric): string {
  if (metric.mem_limit === 0) {
    return '—';
  }

  return `${((metric.mem_bytes / metric.mem_limit) * 100).toFixed(1)}%`;
}

function formatDowntime(secs: number): string {
  if (secs < 1) {
    return `${Math.round(secs * 1000)}ms`;
  }

  return `${secs.toFixed(1)}s`;
}

function DowntimeTrend({ deploys }: { deploys: Deploy[] }) {
  const probed = deploys
    .filter((deploy) => deploy.downtime_secs !== null)
    .slice(0, 20)
    .reverse();

  if (probed.length === 0) {
    return null;
  }

  const max = Math.max(...probed.map((deploy) => deploy.downtime_secs ?? 0), 0.001);

  return (
    <div className="mt-3">
      <p className="text-xs text-gray-500">Downtime per deploy, oldest → newest</p>
      <div aria-label="downtime trend" className="mt-1 flex h-16 items-end gap-1">
        {probed.map((deploy) => (
          <div
            key={deploy.id}
            title={`${deploy.type} #${deploy.id}: ${formatDowntime(deploy.downtime_secs ?? 0)}`}
            className="min-w-2 flex-1 rounded-sm bg-blue-500"
            style={{ height: `${Math.max(8, ((deploy.downtime_secs ?? 0) / max) * 100)}%` }}
          />
        ))}
      </div>
    </div>
  );
}

const inputClass =
  'rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-sm text-gray-900';
const buttonClass =
  'rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50';

export default function ServiceDetail({
  service,
  onBack,
  onServiceChanged,
}: {
  service: ServiceView;
  onBack: () => void;
  onServiceChanged: () => void;
}) {
  const [metrics, setMetrics] = useState<Metric[] | null>(null);
  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [deploys, setDeploys] = useState<Deploy[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [metric, setMetric] = useState('mem');
  const [threshold, setThreshold] = useState('85');
  const [duration, setDuration] = useState('300');
  const [sha, setSha] = useState('');
  const [actor, setActor] = useState('');
  const [outcome, setOutcome] = useState('success');
  const [notes, setNotes] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);

    try {
      const [m, r, d] = await Promise.all([
        fetchMetrics(service.id),
        fetchRules(service.id),
        fetchDeploys(service.id),
      ]);
      setMetrics(m);
      setRules(r);
      setDeploys(d);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [service.id]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleOperationFinished = useCallback(() => {
    void load();
    onServiceChanged();
  }, [load, onServiceChanged]);

  async function handleCreateRule(): Promise<void> {
    setFormError(null);

    const thresholdValue = Number.parseFloat(threshold);
    const durationValue = Number.parseInt(duration, 10);

    if (!Number.isFinite(thresholdValue) || thresholdValue <= 0) {
      setFormError('Threshold must be a positive number.');
      return;
    }

    if (!Number.isInteger(durationValue) || durationValue <= 0) {
      setFormError('Duration must be a positive whole number of seconds.');
      return;
    }

    try {
      await createRule({ service_id: service.id, metric, threshold: thresholdValue, duration_secs: durationValue });
      setThreshold('85');
      setDuration('300');
      await load();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleDeleteRule(id: number): Promise<void> {
    setFormError(null);

    try {
      await deleteRule(id);
      await load();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleRecordDeploy(): Promise<void> {
    setFormError(null);

    if (sha.trim() === '' || actor.trim() === '') {
      setFormError('SHA and actor are required.');
      return;
    }

    try {
      await recordDeploy(service.id, { sha: sha.trim(), actor: actor.trim(), outcome, notes });
      setSha('');
      setActor('');
      setNotes('');
      await load();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <div className="space-y-4">
      <button type="button" onClick={onBack} aria-label="Back to services" className={buttonClass}>
        ← Services
      </button>

      {service.strategy === 'bluegreen' && (
        <CutoverPanel service={service} onFinished={handleOperationFinished} />
      )}

      {service.strategy === 'recreate' && (
        <DeployPanel service={service} onFinished={handleOperationFinished} />
      )}

      {(service.strategy === 'bluegreen' || service.strategy === 'recreate') && (
        <RollbackPanel service={service} onFinished={handleOperationFinished} />
      )}

      {error !== null && (
        <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-4">
          <p className="text-sm font-medium text-red-800">Couldn&apos;t load {service.name}</p>
          <p className="mt-1 text-sm text-red-700">{error}</p>
          <button type="button" onClick={() => void load()} className={`${buttonClass} mt-2`}>
            Retry
          </button>
        </div>
      )}

      {formError !== null && (
        <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-4">
          <p className="text-sm text-red-700">{formError}</p>
        </div>
      )}

      <section aria-label="metrics" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="text-lg font-semibold text-gray-900">Metrics</h2>
        {metrics === null ? (
          <p className="mt-2 text-sm text-gray-500">Loading samples…</p>
        ) : metrics.length === 0 ? (
          <p className="mt-2 text-sm text-gray-500">
            No samples yet — the sampler records every 30 seconds.
          </p>
        ) : (
          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="text-xs text-gray-500">
                  <th className="py-1 pr-4 font-medium">Container</th>
                  <th className="py-1 pr-4 font-medium">CPU</th>
                  <th className="py-1 pr-4 font-medium">Memory</th>
                  <th className="py-1 pr-4 font-medium">Disk</th>
                  <th className="py-1 pr-4 font-medium">Restarts</th>
                  <th className="py-1 font-medium">Uptime</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100">
                {latestByContainer(metrics).map((sample) => (
                  <tr key={sample.container_name} className="text-gray-900">
                    <td className="py-1.5 pr-4 font-medium">{sample.container_name}</td>
                    <td className="py-1.5 pr-4 font-mono">{sample.cpu_percent.toFixed(1)}%</td>
                    <td className="py-1.5 pr-4 font-mono">
                      {formatBytes(sample.mem_bytes)} ({memPercent(sample)})
                    </td>
                    <td className="py-1.5 pr-4 font-mono">{formatBytes(sample.disk_bytes)}</td>
                    <td className="py-1.5 pr-4 font-mono">{sample.restarts}</td>
                    <td className="py-1.5 font-mono">{formatUptime(sample.uptime_secs)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <section aria-label="alert rules" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="text-lg font-semibold text-gray-900">Alert rules</h2>
        {rules === null ? (
          <p className="mt-2 text-sm text-gray-500">Loading rules…</p>
        ) : rules.length === 0 ? (
          <p className="mt-2 text-sm text-gray-500">No rules — breaches will go unnoticed.</p>
        ) : (
          <ul className="mt-2 divide-y divide-gray-100">
            {rules.map((rule) => (
              <li key={rule.id} className="flex flex-wrap items-center gap-2 py-1.5 text-sm">
                <span className="font-mono text-gray-900">
                  {rule.metric} &gt; {rule.threshold} for {rule.duration_secs}s
                </span>
                {!rule.enabled && <span className="text-xs text-gray-500">(disabled)</span>}
                <button
                  type="button"
                  onClick={() => void handleDeleteRule(rule.id)}
                  aria-label={`Delete rule ${rule.id}`}
                  className="ml-auto text-sm text-red-700 hover:underline"
                >
                  Delete
                </button>
              </li>
            ))}
          </ul>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <label className="text-sm text-gray-700">
            Metric{' '}
            <select
              value={metric}
              onChange={(event) => setMetric(event.target.value)}
              className={inputClass}
            >
              <option value="cpu">cpu %</option>
              <option value="mem">mem %</option>
              <option value="disk">disk bytes</option>
            </select>
          </label>
          <label className="text-sm text-gray-700">
            Over{' '}
            <input
              value={threshold}
              onChange={(event) => setThreshold(event.target.value)}
              inputMode="decimal"
              aria-label="Threshold"
              className={`${inputClass} w-24`}
            />
          </label>
          <label className="text-sm text-gray-700">
            For (s){' '}
            <input
              value={duration}
              onChange={(event) => setDuration(event.target.value)}
              inputMode="numeric"
              aria-label="Duration seconds"
              className={`${inputClass} w-24`}
            />
          </label>
          <button type="button" onClick={() => void handleCreateRule()} className={buttonClass}>
            Add rule
          </button>
        </div>
      </section>

      <section aria-label="deploy history" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="text-lg font-semibold text-gray-900">Deploy history</h2>
        {deploys === null ? (
          <p className="mt-2 text-sm text-gray-500">Loading history…</p>
        ) : deploys.length === 0 ? (
          <p className="mt-2 text-sm text-gray-500">Nothing recorded yet.</p>
        ) : (
          <DowntimeTrend deploys={deploys} />
        )}
        {deploys !== null && deploys.length > 0 && (
          <ul className="mt-2 divide-y divide-gray-100">
            {deploys.map((deploy) => (
              <li key={deploy.id} className="py-1.5 text-sm">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-gray-900">{deploy.sha.slice(0, 12)}</span>
                  <span className="text-gray-700">by {deploy.actor}</span>
                  <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700">
                    {deploy.type}
                  </span>
                  <span
                    className={`rounded-full px-2 py-0.5 text-xs font-medium ${
                      deploy.outcome === 'success'
                        ? 'bg-green-100 text-green-800'
                        : 'bg-red-100 text-red-800'
                    }`}
                  >
                    {deploy.outcome}
                  </span>
                  {deploy.duration_secs !== null && (
                    <span className="text-xs text-gray-500">{deploy.duration_secs}s</span>
                  )}
                  {deploy.downtime_secs !== null && (
                    <span className="text-xs text-gray-500">
                      downtime {formatDowntime(deploy.downtime_secs)}
                    </span>
                  )}
                </div>
                {deploy.notes !== '' && <p className="mt-0.5 text-sm text-gray-500">{deploy.notes}</p>}
              </li>
            ))}
          </ul>
        )}
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <input
            value={sha}
            onChange={(event) => setSha(event.target.value)}
            placeholder="Image SHA"
            aria-label="Image SHA"
            className={`${inputClass} w-32`}
          />
          <input
            value={actor}
            onChange={(event) => setActor(event.target.value)}
            placeholder="Actor"
            aria-label="Actor"
            className={`${inputClass} w-28`}
          />
          <label className="text-sm text-gray-700">
            Outcome{' '}
            <select
              value={outcome}
              onChange={(event) => setOutcome(event.target.value)}
              className={inputClass}
            >
              <option value="success">success</option>
              <option value="failure">failure</option>
            </select>
          </label>
          <input
            value={notes}
            onChange={(event) => setNotes(event.target.value)}
            placeholder="Notes (optional)"
            aria-label="Notes"
            className={`${inputClass} flex-1`}
          />
          <button type="button" onClick={() => void handleRecordDeploy()} className={buttonClass}>
            Record deploy
          </button>
        </div>
      </section>
    </div>
  );
}
