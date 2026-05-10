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
