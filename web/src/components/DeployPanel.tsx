import { useCallback, useState } from 'react';
import { useOperationEvents } from '../hooks/useOperationEvents.ts';
import { startDeploy, type ServiceView } from '../lib/api.ts';

export default function DeployPanel({
  service,
  onFinished,
}: {
  service: ServiceView;
  onFinished: () => void;
}) {
  const [confirmed, setConfirmed] = useState(false);
  const [resolvedTarget, setResolvedTarget] = useState<string | null>(null);
  const [streaming, setStreaming] = useState(false);
  const [startError, setStartError] = useState<string | null>(null);

  const handleStreamFinished = useCallback(() => {
    setStreaming(false);
    onFinished();
  }, [onFinished]);

  const handleStreamError = useCallback(() => {
    setStreaming(false);
  }, []);

  const { lines, streamError, clear } = useOperationEvents({
    serviceId: service.id,
    active: streaming,
    finishedType: 'deploy_finished',
    operationNoun: 'deploy',
    onFinished: handleStreamFinished,
    onStreamError: handleStreamError,
  });

  async function handleStart(): Promise<void> {
    setStartError(null);
    clear();
    setResolvedTarget(null);

    try {
      const response = await startDeploy(service.id);
      setResolvedTarget(response.target);
      setConfirmed(false);
      setStreaming(true);
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section aria-label="deploy" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
      <h2 className="text-lg font-semibold text-gray-900">Deploy</h2>
      <p className="mt-1 text-sm text-gray-600">
        {service.name} recreates its container in place, so expect brief downtime — the public URL
        is probed during the run and the seconds land in deploy history.
      </p>

      {startError !== null && (
        <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
          <p className="text-sm text-red-700">{startError}</p>
        </div>
      )}

      {streamError !== null && (
        <div role="alert" className="mt-3 rounded-lg border border-red-200 bg-red-50 p-3">
          <p className="text-sm text-red-700">{streamError}</p>
        </div>
      )}

      <label className="mt-3 flex items-start gap-2 text-sm text-gray-700">
        <input
          type="checkbox"
          checked={confirmed}
          disabled={streaming}
          onChange={(event) => setConfirmed(event.target.checked)}
          className="mt-1"
        />
        <span>I confirm redeploying {service.name} (brief downtime expected).</span>
      </label>

      <div className="mt-3">
        <button
          type="button"
          onClick={() => void handleStart()}
          disabled={!confirmed || streaming}
          aria-label="Start deploy"
          className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
        >
          {streaming ? 'Deploy running…' : 'Start deploy'}
        </button>
      </div>

      {resolvedTarget !== null && (
        <p className="mt-2 text-sm text-gray-700">
          Accepted: redeploying {service.name} ({resolvedTarget}).
        </p>
      )}

      {lines.length > 0 && (
        <ul
          aria-label="deploy progress"
          aria-live="polite"
          className="mt-3 max-h-56 space-y-1 overflow-y-auto rounded-md bg-gray-900 p-3 font-mono text-xs text-gray-100"
        >
          {lines.map((line, index) => (
            <li key={`${index}-${line}`}>{line}</li>
          ))}
        </ul>
      )}
    </section>
  );
}
