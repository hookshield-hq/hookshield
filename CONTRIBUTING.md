# Contributing to HookShield

Thanks for helping! This document is short on purpose — please read it all.

## Definition of Done

A change is only done when **all** of these are true:

1. **Tests** — new behaviour is covered by tests; bug fixes include a test that fails without the fix. `make test` and `make test-integration` pass, and `make up && make demo` still works end to end. Dashboard changes: `make web-test` and `make web-e2e` pass.
2. **Documentation** — public behaviour, config variables, API routes and architecture changes are documented in the same PR (see table below). Exported Go identifiers have doc comments.
3. **i18n** — every user-facing string in the dashboard, emails and alerts exists in **en**, **pt-BR** and **es**. The API returns error *codes* (e.g. `endpoint.not_found`), never sentences; each new code gets an `errors.<code>` entry in `web/messages/*.json`. In the dashboard no text is written in JSX: use `useTranslations`. `npm run check:i18n` (in `make web-check` and CI) enforces all of this.
4. **Lint** — `make lint` is clean (and `make web-check` for dashboard changes).
5. **Security** — no secrets in code, logs or fixtures.

## Where documentation lives

| Change | Update |
|---|---|
| New/changed environment variable | `.env.example` + `docs/CONFIGURATION.md` |
| New/changed API route | `backend/internal/api/openapi.json` (a test fails until path, method and `x-access` match), `docs/API.md`, an integration test in `internal/api`, and `make gen-api` to refresh the dashboard types (CI fails if they are stale) |
| Ingest behaviour (status codes, auth, limits) | `docs/INGEST.md` — it is a public contract for webhook senders |
| Transform rules (ops, paths, validation codes) | `docs/TRANSFORM.md` — public reference for users |
| Delivery behaviour (headers, retries, statuses, SSRF) | `docs/DELIVERY.md` — public contract for receiving applications |
| Alerts (channels, payload, cooldown, messages) | `docs/ALERTS.md`; new message keys in **all three languages** |
| Architecture, queues, data flow | `docs/ARCHITECTURE.md` |
| Something the cloud may restrict or bill | an interface in `backend/core` with a self-hosted default + `docs/ARCHITECTURE.md` (editions) — never a payment-provider import |
| Database schema | migration file + `docs/DATABASE.md` |
| Dev workflow / commands | `docs/DEVELOPMENT.md` + `Makefile` help text |
| UI / design tokens | `docs/DOCS_UI_SPEC.md` + `web/src/app/globals.css`; dashboard conventions in `web/README.md` |
| Significant decision | `docs/adr/NNNN-title.md` |

## Testing conventions (Go)

- Table-driven tests, standard library `testing` package.
- Unit tests next to the code (`foo_test.go`).
- Integration tests that need Postgres/Redis use the `integration` build tag and run with `make dev-up`.
- Always run with the race detector (`make test` does).

## Testing conventions (dashboard)

- Unit tests next to the code (`foo.test.ts`, Vitest) for logic: formatting, error mapping, validation.
- Flows the user goes through (sign-in, navigation, language switch) are Playwright tests in `web/tests/e2e`, against the real API.
- Select by role and accessible name; filter to visible elements (`.filter({ visible: true })`), because Next keeps visited routes mounted but hidden.

## Commits

[Conventional Commits](https://www.conventionalcommits.org/): `feat(ingest): verify endpoint token`, `fix(worker): …`, `docs: …`, `test: …`, `chore: …`.

## Pull requests

Small and focused. Describe *what* and *why*, link the roadmap item, and include screenshots (in all three languages) for UI changes.
