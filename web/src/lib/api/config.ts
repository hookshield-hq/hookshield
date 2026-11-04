// The API base URL is read at runtime from /api/config (see
// src/app/api/config/route.ts), so one build serves any deployment.
// NEXT_PUBLIC_API_URL, when set at build time, skips the request.

let pending: Promise<string> | null = null;

export function apiBaseURL(): Promise<string> {
  const fixed = process.env.NEXT_PUBLIC_API_URL;
  if (fixed) return Promise.resolve(fixed.replace(/\/+$/, ""));
  pending ??= fetch("/api/config", { cache: "no-store" })
    .then((r) => r.json() as Promise<{ apiUrl: string }>)
    .then((c) => c.apiUrl)
    .catch((err) => {
      pending = null; // retry on the next call
      throw err;
    });
  return pending;
}

/** Test helper. */
export function resetApiBaseURL() {
  pending = null;
}
