import type { ApplyState } from '../../types';
import { formatTimestamp, totalChanges } from '../../utils/format';
import { RouterResultList, WarningList } from '../PlanSummary';

interface ApplyResultModalProps {
  state: ApplyState;
  onClose: () => void;
}

/** The outcome of an apply: per router, and the warnings once. */
export default function ApplyResultModal({ state, onClose }: ApplyResultModalProps) {
  const result = state.status === 'done' ? state.result : null;
  const routers = result?.results ?? [];
  const failed = routers.filter(r => !r.success);

  const title =
    state.status === 'running' ? 'Applying the policy...'
    : state.status === 'error' ? 'Apply failed'
    : result?.success ? 'Policy applied'
    : 'Policy partially applied';

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-3xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-lg font-bold text-gray-800">{title}</h2>
        </div>

        <div className="overflow-y-auto max-h-[55vh] p-6 space-y-4">
          {state.status === 'running' && (
            <p className="text-sm text-gray-500">
              The routers are being updated. This can take up to a minute if a router does not answer.
            </p>
          )}
          {state.status === 'error' && (
            <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
              {state.message}
            </p>
          )}
          {result && (
            <>
              {result.success ? (
                <p className="text-sm text-green-800 bg-green-50 border border-green-200 rounded-lg px-3 py-2">
                  {totalChanges(routers) > 0
                    ? `All ${routers.length} router(s) now run the policy.`
                    : 'Nothing had to be changed: the routers already ran the policy.'}
                  {result.applied_at && ` Applied at ${formatTimestamp(result.applied_at)}.`}
                </p>
              ) : (
                <div className="text-sm text-red-800 bg-red-50 border border-red-200 rounded-lg px-3 py-2">
                  <p className="font-medium">
                    Partially applied: {failed.length} of {routers.length} router(s) failed. The draft
                    is not recorded as applied; fix the problem and apply again.
                  </p>
                  <ul className="list-disc pl-5 mt-1 space-y-0.5 text-xs">
                    {failed.map(r => (
                      <li key={`${r.router}|${r.address}`} className="break-words">
                        <span className="font-semibold">{r.router}</span>
                        {r.error ? `: ${r.error}` : ''}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              <WarningList warnings={result.warnings} />
              <RouterResultList routers={routers} mode="result" />
            </>
          )}
        </div>

        <div className="p-6 border-t flex justify-end gap-3">
          <button
            onClick={onClose}
            disabled={state.status === 'running'}
            className="px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700 disabled:bg-gray-300"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
