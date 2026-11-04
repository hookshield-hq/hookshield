/**
 * Content-Security-Policy of the dashboard pages. Read per request because the
 * API origin comes from HS_API_URL at runtime (one image serves every
 * install). Scripts and styles may only come from this origin; the API is the
 * only other place the browser may connect to. 'unsafe-inline' is needed for
 * the inline scripts Next.js streams with every page (nonces would force every
 * page to render dynamically); everything else is locked down.
 */
export function contentSecurityPolicy(apiURL: string | undefined, dev = false): string {
  let api = "";
  try {
    api = apiURL ? new URL(apiURL).origin : "";
  } catch {
    api = "";
  }
  const connect = ["'self'", api, dev ? "ws: wss:" : ""].filter(Boolean).join(" ");
  return [
    "default-src 'self'",
    `script-src 'self' 'unsafe-inline'${dev ? " 'unsafe-eval'" : ""}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    `connect-src ${connect}`,
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
  ].join("; ");
}
