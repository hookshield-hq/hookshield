import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./schema";
import { apiBaseURL } from "./config";
import { ApiError, NETWORK_ERROR, toApiError } from "./errors";

export type { paths, components } from "./schema";

type Client = ReturnType<typeof createClient<paths>>;

let client: Promise<Client> | null = null;

// Turns every non-2xx response into an ApiError, so callers only handle data.
const throwOnError: Middleware = {
  async onResponse({ response }) {
    if (response.ok) return undefined;
    let body: unknown;
    try {
      body = await response.clone().json();
    } catch {
      body = undefined;
    }
    throw toApiError(response.status, body);
  },
  onError({ error }) {
    if (error instanceof ApiError) return error;
    return new ApiError(0, NETWORK_ERROR);
  },
};

/**
 * The typed API client (generated from backend/internal/api/openapi.json —
 * regenerate with `npm run gen:api`). Requests carry the dashboard session
 * cookie; the API checks the Origin header against HS_CORS_ORIGINS.
 */
export function api(): Promise<Client> {
  client ??= apiBaseURL().then((baseUrl) => {
    const c = createClient<paths>({ baseUrl, credentials: "include" });
    c.use(throwOnError);
    return c;
  });
  return client.catch((err) => {
    client = null;
    throw err instanceof ApiError ? err : new ApiError(0, NETWORK_ERROR);
  });
}

/** Unwraps an openapi-fetch result (errors were already thrown by the middleware). */
export function data<T>(r: { data?: T }): T {
  return r.data as T;
}
