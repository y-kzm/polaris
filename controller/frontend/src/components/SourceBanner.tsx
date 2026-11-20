import { AlertTriangle } from 'lucide-react';
import type { SourcesStatus } from '../types';
import { formatTimestamp } from '../utils/format';

interface SourceBannerProps {
  /** Which sources feed the data shown below the banner. */
  kind: 'neighbors' | 'endpoints';
  sources: SourcesStatus | null;
  /** Why GET /api/sources itself failed, if it did. */
  sourcesError?: string | null;
}

/**
 * A non-blocking warning for every failing source. A source that was not
 * asked yet reports neither success nor an error and is not mentioned.
 */
export default function SourceBanner({ kind, sources, sourcesError }: SourceBannerProps) {
  const lines: { key: string; text: string }[] = [];

  if (sourcesError) {
    lines.push({ key: 'sources', text: `The state of the sources is unknown: ${sourcesError}` });
  }
  if (kind === 'neighbors') {
    for (const s of sources?.neighbors ?? []) {
      if (s.ok || !s.error) continue;
      const kept = s.count > 0
        ? `The ${s.count} host(s) of its last successful fetch are still listed`
        : 'None of its hosts are listed';
      lines.push({
        key: s.url,
        text: `Neighbor source ${s.name || s.url} is failing: ${s.error}. ${kept}; `
          + `last successful fetch: ${formatTimestamp(s.last_success)}.`,
      });
    }
  } else {
    for (const s of sources?.endpoints ?? []) {
      if (s.ok || !s.error) continue;
      lines.push({
        key: s.url,
        text: `Endpoint source ${s.url} is failing: ${s.error}. Its services and prefixes may be `
          + `missing or out of date; last successful answer: ${formatTimestamp(s.last_success)}.`,
      });
    }
  }

  if (lines.length === 0) return null;
  return (
    <div className="mb-4 p-3 rounded-lg border border-yellow-300 bg-yellow-50 space-y-1" role="status">
      {lines.map(line => (
        <div key={line.key} className="flex items-start gap-2 text-xs text-yellow-800">
          <AlertTriangle className="w-3.5 h-3.5 flex-shrink-0 mt-0.5 text-yellow-600" />
          <span className="break-words min-w-0">{line.text}</span>
        </div>
      ))}
    </div>
  );
}
