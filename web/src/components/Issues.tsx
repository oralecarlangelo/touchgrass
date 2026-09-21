import { useCallback, useEffect, useState } from 'react';
import {
  createIssueRule,
  deleteIssueRule,
  fetchIssue,
  fetchIssueLogs,
  fetchIssueRules,
  fetchIssues,
  isUnauthorized,
  type Issue,
  type IssueRule,
  type LogLine,
  type Occurrence,
  type ServiceView,
} from '../lib/api.ts';

function formatTimestamp(value: string): string {
  const parsed = new Date(value);

  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  return parsed.toLocaleString();
}

function bucketByDay(occurrences: Occurrence[]): { label: string; count: number }[] {
  const days: { key: string; label: string; count: number }[] = [];
  const now = new Date();

  for (let i = 13; i >= 0; i -= 1) {
    const day = new Date(now);
    day.setDate(now.getDate() - i);
    const key = day.toISOString().slice(0, 10);
    days.push({ key, label: key.slice(5), count: 0 });
  }

  for (const occurrence of occurrences) {
    const bucket = days.find((day) => day.key === occurrence.created_at.slice(0, 10));

    if (bucket !== undefined) {
      bucket.count += 1;
    }
  }

  return days;
}

function FrequencyStrip({ occurrences }: { occurrences: Occurrence[] }) {
  const days = bucketByDay(occurrences);
  const max = Math.max(...days.map((day) => day.count), 1);

  return (
    <div className="mt-3">
      <p className="text-xs text-gray-500">Frequency, last 14 days (from recent occurrences)</p>
      <div aria-label="occurrence frequency" className="mt-1 flex h-16 items-end gap-1">
        {days.map((day) => (
          <div
            key={day.label}
            title={`${day.label}: ${day.count}`}
            className="min-w-2 flex-1 rounded-sm bg-purple-500"
            style={{ height: `${Math.max(8, (day.count / max) * 100)}%` }}
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

export default function Issues({
  services,
  onUnauthorized,
}: {
  services: ServiceView[];
  onUnauthorized: () => void;
}) {
  const [serviceId, setServiceId] = useState('');
  const [issues, setIssues] = useState<Issue[] | null>(null);
  const [rules, setRules] = useState<IssueRule[] | null>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const [detail, setDetail] = useState<{ issue: Issue; occurrences: Occurrence[] } | null>(null);
  const [linkedLogs, setLinkedLogs] = useState<LogLine[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [kind, setKind] = useState('new_issue');
  const [threshold, setThreshold] = useState('10');
  const [windowSecs, setWindowSecs] = useState('300');
  const [formError, setFormError] = useState<string | null>(null);

  const effective = serviceId !== '' ? serviceId : (services[0]?.id ?? '');

  const load = useCallback(async () => {
    if (effective === '') {
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const [fetchedIssues, fetchedRules] = await Promise.all([
        fetchIssues(effective),
        fetchIssueRules(effective),
      ]);
      setIssues(fetchedIssues);
      setRules(fetchedRules);
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [effective, onUnauthorized]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    setSelected(null);
    setDetail(null);
    setLinkedLogs(null);
  }, [effective]);

  const loadDetail = useCallback(
    async (id: number) => {
      setError(null);
      setDetail(null);
      setLinkedLogs(null);

      try {
        const [fetchedDetail, fetchedLogs] = await Promise.all([
          fetchIssue(id),
          fetchIssueLogs(id),
        ]);
        setDetail(fetchedDetail);
        setLinkedLogs(fetchedLogs);
        setSelected(id);
      } catch (err) {
        if (isUnauthorized(err)) {
          onUnauthorized();
          return;
        }

        setError(err instanceof Error ? err.message : String(err));
      }
    },
    [onUnauthorized],
  );

  async function handleCreateRule(): Promise<void> {
    setFormError(null);

    const thresholdValue = Number.parseInt(threshold, 10);
    const windowValue = Number.parseInt(windowSecs, 10);

    if (kind === 'spike' && (!Number.isInteger(thresholdValue) || thresholdValue < 1)) {
      setFormError('Spike threshold must be a positive whole number.');
      return;
    }

    if (!Number.isInteger(windowValue) || windowValue < 1) {
      setFormError('Window must be a positive whole number of seconds.');
      return;
    }

    try {
      await createIssueRule({
        service_id: effective,
        kind,
        threshold: kind === 'spike' ? thresholdValue : 0,
        window_secs: windowValue,
      });
      setThreshold('10');
      setWindowSecs('300');
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setFormError(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleDeleteRule(id: number): Promise<void> {
    setFormError(null);

    try {
      await deleteIssueRule(id);
      await load();
    } catch (err) {
      if (isUnauthorized(err)) {
        onUnauthorized();
        return;
      }

      setFormError(err instanceof Error ? err.message : String(err));
    }
  }

  const newest = detail?.occurrences[0] ?? null;

  return (
    <div className="space-y-4">
      <section aria-label="issues" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold text-gray-900">Issues</h2>
          <label className="ml-auto text-sm text-gray-700">
            Service{' '}
            <select
              value={effective}
              onChange={(event) => setServiceId(event.target.value)}
              className={inputClass}
            >
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
            disabled={loading || effective === ''}
            aria-label="Refresh issues"
            className={buttonClass}
          >
            {loading ? 'Checking…' : 'Refresh'}
          </button>
        </div>

        <p className="mt-1 text-sm text-gray-600">
          Same-bug reports group by fingerprint; releases tracked per issue.
        </p>

        {error !== null && (
          <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
            <p className="text-sm text-red-700">{error}</p>
          </div>
        )}

        {formError !== null && (
          <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
            <p className="text-sm text-red-700">{formError}</p>
          </div>
        )}

        {issues === null ? (
          <p className="mt-3 text-sm text-gray-500">Loading issues…</p>
        ) : issues.length === 0 ? (
          <p className="mt-3 text-sm text-gray-500">No issues — ship it. 🚢</p>
        ) : (
          <ul className="mt-3 divide-y divide-gray-100">
            {issues.map((issue) => (
              <li key={issue.id}>
                <button
                  type="button"
                  onClick={() => void loadDetail(issue.id)}
                  aria-label={`Open issue ${issue.id}`}
                  className="w-full py-2 text-left"
                >
                  <span className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="font-medium text-gray-900">{issue.title}</span>
                    <span className="rounded-full bg-purple-100 px-2 py-0.5 text-xs font-medium text-purple-800">
                      ×{issue.count}
                    </span>
                    {issue.releases.map((release) => (
                      <span
                        key={release}
                        className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700"
                      >
                        {release}
                      </span>
                    ))}
                    <span className="ml-auto font-mono text-xs text-gray-500">
                      {formatTimestamp(issue.last_seen)}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      {selected !== null && detail === null && error === null && (
        <p className="text-sm text-gray-500">Loading issue…</p>
      )}

      {detail !== null && (
        <section aria-label="issue detail" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-lg font-semibold text-gray-900">{detail.issue.title}</h2>
            <button
              type="button"
              onClick={() => {
                setSelected(null);
                setDetail(null);
                setLinkedLogs(null);
              }}
              aria-label="Close issue detail"
              className={`${buttonClass} ml-auto`}
            >
              Close
            </button>
          </div>

          <p className="mt-1 font-mono text-xs text-gray-500">
            {detail.issue.fingerprint} · first {formatTimestamp(detail.issue.first_seen)} · last{' '}
            {formatTimestamp(detail.issue.last_seen)} · {detail.issue.count} occurrences
          </p>

          <FrequencyStrip occurrences={detail.occurrences} />

          <div className="mt-3">
            <h3 className="text-sm font-semibold text-gray-900">
              Surrounding logs (±60s of newest occurrence)
            </h3>
            {linkedLogs === null ? (
              <p className="mt-1 text-sm text-gray-500">Loading linked logs…</p>
            ) : linkedLogs.length === 0 ? (
              <p className="mt-1 text-sm text-gray-500">
                No log lines in the window — the collector may not cover this service yet.
              </p>
            ) : (
              <ul
                aria-label="logs around newest occurrence"
                className="mt-1 max-h-56 space-y-0.5 overflow-y-auto rounded-md bg-gray-900 p-3 font-mono text-xs"
              >
                {linkedLogs.map((line) => (
                  <li key={line.id} className="flex gap-2">
                    <span className="shrink-0 text-gray-500">{formatTimestamp(line.ts)}</span>
                    <span
                      className={
                        line.stream === 'stderr' ? 'shrink-0 text-red-400' : 'shrink-0 text-gray-500'
                      }
                    >
                      {line.stream}
                    </span>
                    <span className="break-all text-gray-100">{line.line}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {newest !== null && newest.stack.length > 0 && (
            <div className="mt-3">
              <h3 className="text-sm font-semibold text-gray-900">Trace (newest occurrence)</h3>
              <ul
                aria-label="stack trace"
                className="mt-1 max-h-56 space-y-1 overflow-y-auto rounded-md bg-gray-900 p-3 font-mono text-xs text-gray-100"
              >
                {newest.stack.map((frame, index) => (
                  <li key={`${index}-${frame.file}-${frame.line}`}>
                    {frame.function} ({frame.file}:{frame.line}:{frame.column})
                  </li>
                ))}
              </ul>
            </div>
          )}

          <div className="mt-3">
            <h3 className="text-sm font-semibold text-gray-900">
              Occurrences ({detail.occurrences.length})
            </h3>
            {detail.occurrences.length === 0 ? (
              <p className="mt-1 text-sm text-gray-500">None retained.</p>
            ) : (
              <ul className="mt-1 max-h-64 divide-y divide-gray-100 overflow-y-auto">
                {detail.occurrences.map((occurrence) => (
                  <li key={occurrence.id} className="py-1.5 text-sm">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-700">
                        {occurrence.type}
                      </span>
                      <span className="text-gray-900">{occurrence.message}</span>
                      {occurrence.release !== '' && (
                        <span className="font-mono text-xs text-gray-500">{occurrence.release}</span>
                      )}
                      <span className="ml-auto font-mono text-xs text-gray-500">
                        {formatTimestamp(occurrence.created_at)}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </section>
      )}

      <section aria-label="issue alert rules" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
        <h2 className="text-lg font-semibold text-gray-900">Issue alert rules</h2>
        {rules === null ? (
          <p className="mt-2 text-sm text-gray-500">Loading rules…</p>
        ) : rules.length === 0 ? (
          <p className="mt-2 text-sm text-gray-500">No rules — new issues will slip by quietly.</p>
        ) : (
          <ul className="mt-2 divide-y divide-gray-100">
            {rules.map((rule) => (
              <li key={rule.id} className="flex flex-wrap items-center gap-2 py-1.5 text-sm">
                <span className="font-mono text-gray-900">
                  {rule.kind === 'spike'
                    ? `spike ≥ ${rule.threshold} in ${rule.window_secs}s`
                    : `new issue within ${rule.window_secs}s`}
                </span>
                {!rule.enabled && <span className="text-xs text-gray-500">(disabled)</span>}
                <button
                  type="button"
                  onClick={() => void handleDeleteRule(rule.id)}
                  aria-label={`Delete issue rule ${rule.id}`}
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
            Kind{' '}
            <select
              value={kind}
              onChange={(event) => setKind(event.target.value)}
              className={inputClass}
            >
              <option value="new_issue">new issue</option>
              <option value="spike">spike</option>
            </select>
          </label>
          {kind === 'spike' && (
            <label className="text-sm text-gray-700">
              At least{' '}
              <input
                value={threshold}
                onChange={(event) => setThreshold(event.target.value)}
                inputMode="numeric"
                aria-label="Spike threshold"
                className={`${inputClass} w-24`}
              />
            </label>
          )}
          <label className="text-sm text-gray-700">
            Within (s){' '}
            <input
              value={windowSecs}
              onChange={(event) => setWindowSecs(event.target.value)}
              inputMode="numeric"
              aria-label="Window seconds"
              className={`${inputClass} w-24`}
            />
          </label>
          <button
            type="button"
            onClick={() => void handleCreateRule()}
            disabled={effective === ''}
            className={buttonClass}
          >
            Add rule
          </button>
        </div>
      </section>
    </div>
  );
}
