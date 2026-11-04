# web — HookShield dashboard

Next.js 16 (App Router) · TypeScript · Tailwind CSS v4 · Motion · next-themes · cmdk · next-intl · TanStack Query · openapi-fetch.

A pure frontend: every piece of data comes from the Go control-plane API (`backend/cmd/api`). The browser calls the API directly with the session cookie (`credentials: "include"`); the API allows the dashboard origin through CORS (`HS_APP_URL` / `HS_CORS_ORIGINS`).

## Run it

| Goal | Command (repo root) |
|---|---|
| Everything in Docker, dashboard included | `make up` → http://localhost:3000 |
| Hot reload against the native Go stack | `make dev-up` · `make dev` · then `make web-install` and `make web-dev` in another terminal |
| Production build locally | `make web-build` · `make web-start` |

Create an account on the sign-up page. In development every email (verification, password reset) lands in Mailpit at http://localhost:8025; the account works before the address is verified.

**API URL.** The dashboard reads it at runtime from `GET /api/config`, which returns `HS_API_URL` (default `http://localhost:8081`). One build or Docker image therefore serves every deployment; nothing API-specific is baked in at build time.

## Checks

| Command | What it does |
|---|---|
| `make web-check` | `npm run typecheck` (route types + `tsc`), `npm run lint` (ESLint), `npm run check:i18n` |
| `make web-test` | `web-check` + unit tests (Vitest) |
| `make web-e2e` | Builds, then runs Playwright against `next start` on :3000 (reuses a running server, e.g. the Docker one). Needs the API (`make dev` or `make up`). `a11y.spec.ts` audits every screen with axe-core (WCAG 2.1 AA) in both themes; some tests confirm the account's email through Mailpit (`HS_MAILPIT_URL`, default `http://localhost:8025`). `billing.spec.ts` runs the checkout flow when the API is HookShield Cloud with the fake Paddle (`hookshield-cloud/scripts/dev.sh`); against the core it checks that billing stays hidden. |
| `SCREENSHOTS=1 npx playwright test screenshots` | Saves the login, dashboard and command palette in every language and theme to `test-results/screens/` |

Node 22 or newer. `@playwright/test` is pinned to the exact version whose browsers CI installs; run `npx playwright install chromium` once after `npm ci`.

## Internationalization

- Locales: `en`, `pt-BR`, `es`, always in the URL (`/en/logs`, `/pt-BR/logs`). `/` redirects to the browser's language; the user's choice is saved to the account (`PATCH /v1/me`) and to next-intl's `NEXT_LOCALE` cookie.
- Messages live in `messages/{locale}.json`. **Every user-facing string goes through `useTranslations`** — no literal text in JSX.
- `npm run check:i18n` (also in CI) fails on: keys missing in a locale, empty values, placeholders that differ between locales, backend error codes without a translation under `errors.*`, and literal text or `aria-label`/`placeholder`/`title`/`alt`/`label` strings in components.
- API errors arrive as codes (`{"error":{"code":"auth.invalid_credentials"}}`); `useErrorMessage` maps them to `errors.<code>` with a generic fallback.

## API types

`src/lib/api/schema.d.ts` is generated from the backend's OpenAPI document:

```bash
make gen-api        # = npm run gen:api (reads backend/internal/api/openapi.json)
```

CI regenerates it and fails if the committed file differs, so a backend contract change always comes with the matching frontend types. Use the typed client from `src/lib/api/client.ts`:

```ts
const me = await data(api().GET("/v1/me"));   // typed response, throws ApiError on non-2xx
```

## Layout

```
messages/                  en.json · pt-BR.json · es.json
scripts/check-i18n.mjs     i18n checker (see above)
src/proxy.ts               locale negotiation and redirects (Next 16 proxy)
src/i18n/                  routing, navigation helpers, request config
src/app/api/config/        runtime config (API URL)
src/app/[locale]/(auth)/   login, register, forgot/reset password, verify email
src/app/[locale]/(app)/    authenticated screens: dashboard, endpoints (+ /endpoints/[id]), logs, transform,
                           sandbox, alerts, secrets, api-keys, settings
src/components/ui/         design-system primitives (button, card, input, select, badge, table, tabs, segmented,
                           drawer, modal, confirm, toast, code/JSON viewer, key-value editor…)
src/components/shell/      sidebar, cluster status, command palette (⌘K), user menu, theme/language switchers
src/components/<screen>/   one folder per screen (endpoints, logs, dashboard, transform, sandbox, resources, settings)
src/lib/queries.ts         TanStack Query hooks for every API resource (one place for cache keys and invalidation)
src/lib/live.ts            live feed hook (Server-Sent Events from GET /v1/live)
src/lib/                   API client and errors, auth hooks, formatting, durations, utilities
tests/e2e/                 Playwright tests (auth.spec, screens.spec, screenshots.spec; helpers.ts)
```

## Screens

| Route | What it does |
|---|---|
| `/` | Metric cards, traffic chart (canvas: volume, latency, error rate; 1h–30d), live stream over SSE, first-run onboarding |
| `/endpoints` · `/endpoints/[id]` | List with 24 h traffic and success rate; create (token, HMAC or IP allow-list; the token is shown once); detail tabs: overview, settings, security (rotate), delivery (outbound headers, visual retry policy, DLQ), transform, alerts |
| `/logs` | Filters (endpoint, status, period, search), keyset pagination, live updates; debugger drawer with the event timeline, original/transformed request, every destination response, replay and edit & replay. `?id=` opens a log, `?endpoint=` / `?status=` preset filters |
| `/transform` | Split editor: sample input (or the latest webhook), rule blocks or raw JSON, live output preview from `POST /v1/transform/preview` |
| `/sandbox` | Sends test events through the real pipeline (`POST /v1/endpoints/{id}/test`) and generates cURL / Node.js / Python snippets |
| `/alerts` · `/secrets` · `/api-keys` · `/settings` | Alert channels, encrypted secrets, API keys (shown once), profile, preferences, password and plan |
| `/billing` | HookShield Cloud only (the nav item appears when `GET /v1/features` says `billing: true`): current plan and status, usage of the month, plan catalog, checkout, plan switch, customer portal. After the checkout it polls until the provider's webhook activates the plan |

## Conventions

- **Design tokens** are CSS variables in `src/app/globals.css` (`--hs-*`, dark values are the spec's), exposed to Tailwind as `bg-surface`, `text-muted`, `border-line`, `text-accent`, `bg-success-bg`… Use them instead of raw colours so both themes stay correct.
- **Data fetching** uses the hooks in `src/lib/queries.ts` (TanStack Query around the typed client); never call `fetch` on the API directly. Mutations invalidate what they change there.
- **Secrets are shown once** (endpoint tokens, HMAC secrets, API keys): the reveal window asks the user to confirm they copied the value before it closes, and nothing keeps it afterwards.
- **Authenticated pages** export `instant = false`: the shell renders them only after the session loads in the browser (the API owns the cookie), so Next cannot validate them for instant navigation on the server.
- **Hidden routes keep state.** Next 16 keeps visited routes mounted but hidden (`<Activity>`). Forms holding secrets call `useResetOnHide` so a typed password or token does not survive navigation; the authenticated shell remounts its content when the signed-in user changes.
- Docker: `web/Dockerfile` builds the standalone server (`output: "standalone"`) and runs it as the unprivileged `node` user.
