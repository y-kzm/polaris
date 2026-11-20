import type { PolicyPlan } from '../types';
import { totalChanges } from '../utils/format';

/** One sentence that says what an apply would do. */
export default function PlanVerdict({ plan }: { plan: PolicyPlan }) {
  if (!plan.pending) {
    return (
      <p className="text-sm text-green-800 bg-green-50 border border-green-200 rounded-lg px-3 py-2">
        Nothing to change: the routers already run the current policy.
      </p>
    );
  }
  return (
    <p className="text-sm text-orange-800 bg-orange-50 border border-orange-200 rounded-lg px-3 py-2">
      {totalChanges(plan.routers ?? []) > 0
        ? 'There are unapplied changes. Applying changes the routers as listed below.'
        : 'The draft differs from the applied policy, but no router needs a change. '
          + 'Applying records the draft as the applied policy.'}
    </p>
  );
}
