import { useCallback, useState } from 'react';
import { useOperationEvents } from '../hooks/useOperationEvents.ts';
import { startCutover, type ServiceView } from '../lib/api.ts';

export default function CutoverPanel({
  service,
  onFinished,
}: {
  service: ServiceView;
  onFinished: () => void;
}) {
  const [target, setTarget] = useState('auto');
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
    operationNoun: 'cutover',
    onFinished: handleStreamFinished,
    onStreamError: handleStreamError,
  });

  async function handleStart(): Promise<void> {
    setStartError(null);
    clear();
    setResolvedTarget(null);

    try {
      const response = await startCutover(service.id, target === 'auto' ? '' : target);
      setResolvedTarget(response.target);
      setConfirmed(false);
      setStreaming(true);
    } catch (err) {
      setStartError(err instanceof Error ? err.message : String(err));
    }
  }

  return (
    <section aria-label="cutover" className="rounded-lg border border-gray-200 bg-white p-5 shadow-sm">
      <h2 className="text-lg font-semibold text-gray-900">Cutover</h2>
      <p className="mt-1 text-sm text-gray-600">
        Live color: {service.live_color === '' ? 'unknown' : service.live_color}. The configured
        cutover script runs its preflight checks before traffic moves.
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

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <label className="text-sm text-gray-700">
          Target{' '}
          <select
            value={target}
            onChange={(event) => setTarget(event.target.value)}
            disabled={streaming}
            className="rounded-md border border-gray-300 bg-white px-2.5 py-1.5 text-sm text-gray-900"
          >
            <option value="auto">auto (idle color)</option>
            <option value="blue">blue</option>
            <option value="green">green</option>
          </select>
        </label>
        <button
          type="button"
          onClick={() => void handleStart()}
          disabled={!confirmed || streaming}
          aria-label="Start cutover"
          className="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50"
        >
          {streaming ? 'Cutover running…' : 'Start cutover'}
        </button>
      </div>

      <label className="mt-3 flex items-start gap-2 text-sm text-gray-700">
        <input
          type="checkbox"
          checked={confirmed}
          disabled={streaming}
          onChange={(event) => setConfirmed(event.target.checked)}
          className="mt-1"
        />
        <span>
          I confirm flipping live traffic for {service.name}
          {target === 'auto' ? ' to the idle color' : ` to ${target}`}.
        </span>
      </label>

      {target !== 'auto' && target === service.live_color && (
        <p className="mt-2 text-sm text-amber-700">
          Target is already live. Starting will re-run the cutover against the same color.
        </p>
      )}

      {resolvedTarget !== null && (
        <p className="mt-2 text-sm text-gray-700">
          Accepted: flipping {service.name} to {resolvedTarget}.
        </p>
      )}

      {lines.length > 0 && (
        <ul
          aria-label="cutover progress"
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
