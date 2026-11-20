import type { PolicyPlan } from '../../types';
import { RouterResultList, WarningList } from '../PlanSummary';
import PlanVerdict from '../PlanVerdict';

interface ApplyConfirmModalProps {
  plan: PolicyPlan | null;
  error: string | null;
  loading: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/** Shows what the apply will change on each router before it is confirmed. */
export default function ApplyConfirmModal({ plan, error, loading, onConfirm, onCancel }: ApplyConfirmModalProps) {
  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-3xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b">
          <h2 className="text-lg font-bold text-gray-800">Commit &amp; Deploy Policy</h2>
          <p className="text-sm text-gray-600 mt-1">
            The controller brings every router to the current draft: new RAs first, withdrawals after.
          </p>
        </div>

        <div className="overflow-y-auto max-h-[55vh] p-6 space-y-4">
          {loading && <p className="text-sm text-gray-400">Computing what will change...</p>}
          {error && (
            <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
              The plan could not be computed: {error}
              {plan ? ' — what is shown below may be out of date.' : ' — what an apply would change is unknown.'}
            </p>
          )}
          {plan && (
            <>
              <PlanVerdict plan={plan} />
              <WarningList warnings={plan.warnings} />
              <RouterResultList routers={plan.routers ?? []} mode="plan" />
            </>
          )}
        </div>

        <div className="p-6 border-t flex justify-end gap-3">
          <button onClick={onCancel} className="px-4 py-2 border rounded-lg hover:bg-gray-50 text-sm">
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={loading}
            className="px-4 py-2 rounded-lg text-sm text-white font-medium bg-blue-600 hover:bg-blue-700 disabled:bg-gray-300"
          >
            {plan && !plan.pending ? 'Apply anyway' : 'Commit & Deploy'}
          </button>
        </div>
      </div>
    </div>
  );
}
