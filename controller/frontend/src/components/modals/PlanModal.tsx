import { RefreshCw } from 'lucide-react';
import type { PolicyPlan } from '../../types';
import { formatTimestamp } from '../../utils/format';
import { RouterResultList, WarningList } from '../PlanSummary';
import PlanVerdict from '../PlanVerdict';

interface PlanModalProps {
  plan: PolicyPlan | null;
  error: string | null;
  loading: boolean;
  onRefresh: () => void;
  onClose: () => void;
}

/** What applying the current draft would do, as computed by the backend. */
export default function PlanModal({ plan, error, loading, onRefresh, onClose }: PlanModalProps) {
  const routers = plan?.routers ?? [];
  const participating = routers.filter(r => r.status === 'active').length;

  return (
    <div className="fixed inset-0 bg-black bg-opacity-50 flex items-center justify-center p-4 z-50">
      <div className="bg-white rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] overflow-hidden">
        <div className="p-6 border-b flex items-start justify-between gap-4">
          <div>
            <h2 className="text-xl font-bold">Plan</h2>
            <p className="text-sm text-gray-600 mt-1">
              What applying the current draft would do. Computed by the controller from the
              state of the routers; nothing is changed.
            </p>
          </div>
          <button
            onClick={onRefresh}
            disabled={loading}
            className="flex items-center gap-1.5 px-3 py-1.5 border rounded-lg text-sm hover:bg-gray-50 disabled:opacity-50 flex-shrink-0"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />
            {loading ? 'Computing...' : 'Refresh'}
          </button>
        </div>

        <div className="overflow-y-auto max-h-[60vh] p-6 space-y-4">
          {error && (
            <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 break-words">
              The plan could not be computed: {error}
              {plan && ' — what is shown below is the last plan that could be computed.'}
            </p>
          )}
          {!plan && !error && <p className="text-sm text-gray-400 text-center py-8">Computing the plan...</p>}
          {plan && (
            <>
              <PlanVerdict plan={plan} />
              <div className="text-xs text-gray-500 flex flex-wrap gap-x-4 gap-y-0.5">
                <span>Last applied: {formatTimestamp(plan.applied_at, 'never')}</span>
                <span>
                  Routers taking part in the policy: {participating} of {routers.length}
                  {routers.length > participating && ' (inactive routers are excluded and get their policy RAs withdrawn)'}
                </span>
              </div>
              <WarningList warnings={plan.warnings} />
              <RouterResultList routers={routers} mode="plan" showMessages />
            </>
          )}
        </div>

        <div className="p-6 border-t flex justify-end gap-3">
          <button onClick={onClose} className="px-4 py-2 bg-gray-600 text-white rounded-lg hover:bg-gray-700">
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
