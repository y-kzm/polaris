// Formats an ISO date string as "YYYY/MM/DD HH:MM:SS".
export function formatDateTime(isoString: string): string {
  const d = new Date(isoString);
  const pad = (n: number) => n.toString().padStart(2, '0');
  return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())} `
    + `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

// Maps a neighbor NDP state string to a StatusLed severity level.
export function neighborLedStatus(state: string): 'up' | 'warning' | 'unknown' {
  if (state === 'REACHABLE') return 'up';
  if (state === 'STALE')     return 'warning';
  return 'unknown';
}

// Formats a timestamp of the backend; empty or unparsable input gives `fallback`.
export function formatTimestamp(isoString: string | undefined, fallback = 'never'): string {
  if (!isoString) return fallback;
  return Number.isNaN(new Date(isoString).getTime()) ? fallback : formatDateTime(isoString);
}

// A quick look at a group member while it is typed: does it look like a
// link-local IPv6 address (fe80::/10)? This only produces a hint. The backend
// decides, and stores the canonical form.
export function looksLinkLocal(address: string): boolean {
  return /^fe[89ab][0-9a-f]:[0-9a-f:]*$/i.test(address.trim());
}

// Removes duplicates, keeping the first occurrence of each line.
export function uniqueLines(lines: string[] | null | undefined): string[] {
  return Array.from(new Set(lines ?? []));
}

// Number of changes a plan or an apply result reports over all routers.
export function totalChanges(
  routers: { added: number; updated: number; deleted: number; withdrawn: number }[],
): number {
  return routers.reduce((n, r) => n + r.added + r.updated + r.deleted + r.withdrawn, 0);
}

// What the Active/Inactive switch of a router does.
export const INACTIVE_EXPLANATION =
  'Inactive: excluded from the policy; its policy RAs are withdrawn at the next apply';
