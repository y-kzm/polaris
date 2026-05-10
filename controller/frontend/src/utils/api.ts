// Returns the base URL for the backend API.
// Empty string = use relative paths (Vite proxy forwards /api → backend).
export function getApiUrl(): string {
  return import.meta.env.VITE_API_URL ?? '';
}
