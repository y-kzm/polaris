// Returns the base URL for the backend API.
// Empty string = use relative paths (same origin; in development the Vite
// proxy forwards /api → backend).
export function getApiUrl(): string {
  return import.meta.env.VITE_API_URL ?? '';
}

/** A non-2xx answer of the backend. `message` is written for the administrator. */
export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

interface ApiOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  /** Sent as JSON when given. */
  body?: unknown;
}

function errorField(data: unknown): string {
  if (typeof data !== 'object' || data === null) return '';
  const value = (data as Record<string, unknown>).error;
  return typeof value === 'string' ? value : '';
}

/**
 * The one way this UI talks to the backend.
 *
 * Resolves with the decoded JSON body of any 2xx answer (undefined when the
 * body is empty, e.g. 204). That includes 207, which POST /api/policy/apply
 * uses for "some router failed": the body still is the result.
 * Rejects with an ApiError carrying the backend's `error` message (or
 * `HTTP <status>` if there is none) for every other status.
 */
export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  let body: string | undefined;
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(options.body);
  }

  let res: Response;
  try {
    res = await fetch(`${getApiUrl()}${path}`, { method: options.method ?? 'GET', headers, body });
  } catch (e) {
    throw new Error(`The controller backend cannot be reached (${errorMessage(e)})`);
  }

  const text = await res.text();
  let data: unknown;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }

  if (!res.ok) {
    throw new ApiError(res.status, errorField(data) || `HTTP ${res.status}`);
  }
  return data as T;
}

/** The message of anything that was thrown. */
export function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
