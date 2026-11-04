# HookShield website

The public website at **[hookshield.dev](https://hookshield.dev)**: the landing page (English, Português, Español) and the documentation. It is a Next.js app exported to **static HTML** — no server, no cookies, no tracking — published to GitHub Pages by [`.github/workflows/website.yml`](../.github/workflows/website.yml).

## Commands

```bash
make site-install      # npm ci
make site-dev          # http://localhost:3100 with hot reload
make site-build        # static export into website/out
make site-serve        # serve website/out like a static host (404 page, /path → /path/)
make site-test         # type-check, lint, i18n, unit tests, build, Playwright (links + accessibility)
make site-shots        # refresh the dashboard screenshots (see below)
```

## Where things are

| Path | What |
|---|---|
| `src/app/(en)/` | English pages: `/` (landing), `/docs/…`, `/legal/…` — one root layout |
| `src/app/[locale]/` | Translated landing pages `/pt-BR/` and `/es/` — the other root layout |
| `src/app/global-not-found.tsx` | The 404 page (`out/404.html`) |
| `src/components/landing.tsx` | Every section of the landing page |
| `messages/{en,pt-BR,es}.json` | Every label of the website, in the three languages (guide D7) |
| `src/lib/docs.ts` | The docs: sidebar order (`docsNav`) and the Markdown renderer |
| `src/pricing.json` | Cloud plans shown in the pricing section |
| `content/legal/*.md` | Terms of Service and Privacy Policy of HookShield Cloud |
| `public/screens/` | Dashboard screenshots (`<screen>-<theme>-<locale>.webp`) |
| `scripts/sync-assets.mjs` | Copies the logos and icons from `docs/assets` and `web/src/app` into `public/` before every build |

## The docs are the repository's Markdown

The docs pages are rendered at build time from `docs/*.md`, `SECURITY.md` and `CONTRIBUTING.md` — the same files people read on GitHub. Edit them there; never copy them into the website.

- **Adding a page:** create the Markdown file, then add it to `docsNav` in `src/lib/docs.ts` (slug, file, sidebar label). Its first `# heading` is the page title; its first paragraph is the meta description.
- **Links** are written as in the repository (`[Delivery](DELIVERY.md#retries)`). Links to another docs page become site links; links to any other file go to GitHub. `npm test` fails if a link points to a missing page, file or heading, so broken links never ship.
- Code blocks are highlighted for both themes (Shiki) at build time.

## Translations

The landing page and the site chrome exist in English, Portuguese and Spanish. `npm run check:i18n` fails when the three files disagree (keys, placeholders) or when a component contains hard-coded text. The docs and the legal pages are English only (guide D10).

## Pricing

`src/pricing.json` must match the plans HookShield Cloud enforces and bills. The private repository tests this: `hookshield-cloud/backend/internal/plans` compares its plan table with this file, so a price change in one place fails CI until the other is updated.

## Screenshots

`public/screens/` holds real screenshots of the dashboard, in both themes and the three languages. To refresh them after a UI change:

```bash
make dev-up && make dev          # terminal 1: the Go services
make web-build && make web-start # terminal 2: the dashboard on :3000
make site-shots                  # terminal 3: ~5 minutes (sends realistic traffic first)
```

The spec is `web/tests/e2e/landing-shots.spec.ts`; it encodes WebP in the browser, so no image tool is needed.

## Legal pages

`content/legal/terms.md` and `privacy.md` are **drafts** (`draft: true` in the front matter): the page shows a banner and is not indexed. Before HookShield Cloud takes payments, fill in the bracketed fields (legal entity, address, providers, jurisdiction), have them reviewed, and set `draft: false`.

## Publishing

`.github/workflows/website.yml` builds and deploys on every push to `main` that touches the website or the docs. One-time setup of the repository:

1. **Settings → Pages → Build and deployment → Source: GitHub Actions.**
2. **Custom domain:** `hookshield.dev`, then **Enforce HTTPS**.
3. DNS of `hookshield.dev`: `A` records to `185.199.108.153`, `185.199.109.153`, `185.199.110.153`, `185.199.111.153` and `AAAA` to `2606:50c0:8000::153`, `2606:50c0:8001::153`, `2606:50c0:8002::153`, `2606:50c0:8003::153` (GitHub Pages), plus `www` as a `CNAME` to `hookshield-hq.github.io`.

Any other static host works too: upload `website/out` (it needs `404.html` as the not-found page). Build-time variables: `NEXT_PUBLIC_SITE_URL` (canonical origin, default `https://hookshield.dev`), `NEXT_PUBLIC_APP_URL` (dashboard, default `https://app.hookshield.dev`), `NEXT_PUBLIC_RAILWAY_TEMPLATE_URL` (the "Deploy on Railway" button appears only when it is set — add it to `website.yml` once the template is published).
