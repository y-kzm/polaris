import { useCallback, useEffect, useRef, useState } from 'react';
import type { PolicyPlan } from '../types';
import { api, errorMessage } from '../utils/api';

/** A burst of edits within this time makes one plan request. */
const PLAN_DEBOUNCE_MS = 400;

export interface PolicyPlanState {
  /** The last plan the backend computed; null until the first answer. */
  plan: PolicyPlan | null;
  /** Why the last request failed; `plan` may then be out of date. */
  error: string | null;
  loading: boolean;
  /** Ask for a new plan soon (debounced). Call after every mutation. */
  refresh: () => void;
  /** Ask for a new plan now. */
  refreshNow: () => Promise<void>;
}

/**
 * The plan of GET /api/policy/plan: what an apply would do, computed by the
 * backend from the routers' real state. It is fetched once at start and then
 * only on request; there is no timer.
 */
export function usePolicyPlan(): PolicyPlanState {
  const [plan, setPlan] = useState<PolicyPlan | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const latest = useRef(0); // only the answer to the latest request is used

  const refreshNow = useCallback(async () => {
    if (timer.current) {
      clearTimeout(timer.current);
      timer.current = null;
    }
    const request = ++latest.current;
    setLoading(true);
    try {
      const answer = await api<PolicyPlan>('/api/policy/plan');
      if (request !== latest.current) return;
      setPlan(answer);
      setError(null);
    } catch (e) {
      if (request !== latest.current) return;
      setError(errorMessage(e));
    } finally {
      if (request === latest.current) setLoading(false);
    }
  }, []);

  const refresh = useCallback(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => {
      timer.current = null;
      void refreshNow();
    }, PLAN_DEBOUNCE_MS);
  }, [refreshNow]);

  useEffect(() => {
    void refreshNow();
    return () => {
      if (timer.current) clearTimeout(timer.current);
      timer.current = null;
    };
  }, [refreshNow]);

  return { plan, error, loading, refresh, refreshNow };
}
