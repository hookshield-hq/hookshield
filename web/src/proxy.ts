import createMiddleware from "next-intl/middleware";
import type { NextRequest } from "next/server";
import { routing } from "./i18n/routing";
import { contentSecurityPolicy } from "./lib/csp";

const intl = createMiddleware(routing);

// Redirects "/" and unprefixed paths to the best locale (cookie, then
// Accept-Language) and adds the security headers to every page.
export default function proxy(request: NextRequest) {
  const response = intl(request);
  const h = response.headers;
  h.set("Content-Security-Policy", contentSecurityPolicy(process.env.HS_API_URL ?? "http://localhost:8081", process.env.NODE_ENV !== "production"));
  h.set("X-Frame-Options", "DENY");
  h.set("X-Content-Type-Options", "nosniff");
  h.set("Referrer-Policy", "strict-origin-when-cross-origin");
  h.set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()");
  return response;
}

export const config = {
  // Everything except /api (runtime config), Next internals and files with a dot.
  matcher: "/((?!api|_next|_vercel|.*\\..*).*)",
};
