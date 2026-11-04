// Errors from the HookShield API always carry a stable code
// ({"error":{"code":"auth.invalid_credentials"}}). The UI translates the code
// with the "errors" namespace of messages/*.json — never shows server text.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly details?: unknown,
  ) {
    super(code);
    this.name = "ApiError";
  }
}

/** Code used when the API could not be reached at all. */
export const NETWORK_ERROR = "network";

/** Builds an ApiError from a failed response body (or none). */
export function toApiError(status: number, body: unknown): ApiError {
  const err = (body as { error?: { code?: unknown; details?: unknown } } | undefined)?.error;
  const code = typeof err?.code === "string" && err.code ? err.code : status >= 500 ? "internal" : "unknown";
  return new ApiError(status, code, err?.details);
}

/**
 * Translation key for an error code, relative to the "errors" namespace:
 * "auth.invalid_credentials" → "auth.invalid_credentials". Unknown codes fall
 * back to "unknown" so a new backend code never shows a raw key to users.
 */
export function errorKey(code: string, has: (key: string) => boolean): string {
  return has(code) ? code : "unknown";
}
