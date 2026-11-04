# Security

HookShield sits in front of your applications and stores their webhooks, so security is part of every change. This page explains how to report a problem, what HookShield protects against, how to run it safely, and the limitations we know about.

## Reporting a vulnerability

Please **do not open a public issue**. Use GitHub's private reporting instead: **Security → Report a vulnerability** on [hookshield-hq/hookshield](https://github.com/hookshield-hq/hookshield/security). Include what you found, how to reproduce it and the version (`hookshield version`, or the image tag).

We acknowledge reports within 3 business days and keep you updated until a fix is released. We credit reporters in the release notes, unless you prefer otherwise. Please give us a reasonable time to ship a fix before you disclose it publicly.

Supported versions: the latest release. Security fixes are not backported before 1.0.

## What HookShield protects

| Area | Protection |
|---|---|
| Data at rest | Webhook bodies and headers, delivery responses, endpoint HMAC secrets, user secrets and alert targets are encrypted with AES-256-GCM. Each record has a random nonce and AAD tied to its context, so a ciphertext copied elsewhere does not decrypt. Every service checks on boot that its key matches the database. |
| Inbound webhooks | Per-endpoint token (only its SHA-256 is stored; constant-time comparison), HMAC signature or IP allow-list, checked before anything is stored. Unknown endpoints and bad credentials get the same `401`. 1 MiB body limit, per-endpoint rate limit and monthly quota. |
| Outbound deliveries (SSRF) | Checked on the IP actually dialled, after DNS resolution: private, loopback, link-local and metadata, CGNAT, NAT64, 6to4, Teredo, multicast and reserved ranges are refused. So are privileged ports other than 80 and 443. Redirects are not followed, no environment proxy is used, and header values cannot contain line breaks. |
| Dashboard accounts | argon2id passwords, capped concurrency. Google sign-in with state and PKCE; an unverified account is taken over cleanly by the real owner (Google sign-in or password reset): its endpoints, secrets, alert channels, API keys and sessions are deleted. Opaque sessions (stored hashed, sliding plus absolute lifetime) in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie. CSRF protection by `Origin` check. Atomic login throttling, per email and per IP (IPv6 per /64); sign-ups throttled per IP. Forgot-password answers at once (lookup and email in the background), so its timing does not reveal accounts. A password reset revokes every session and API key; a rejected new password does not burn the reset link. |
| API keys | Scopes `read_only`, `ingest`, `admin`. Shown once and stored hashed. Account routes need a dashboard session. |
| Tenancy | Every query is scoped to the account. A resource of another account answers `404`, the same as a missing one. On multi-tenant installs, install-wide numbers are hidden. |
| Abuse | Transform rules have a size budget, so exponential copies are rejected. A message that keeps crashing workers goes to the dead-letter stream after 5 deliveries. Alert emails need a verified account and are capped at 100 per hour per account. Sender-controlled text is sanitised in headers and Slack messages. |
| Browser | Dashboard: Content-Security-Policy (scripts and styles from the site only; connections only to the API), `frame-ancestors 'none'`, `nosniff`, strict referrer policy, permissions policy. API: deny-all CSP, `no-store`. |
| Billing (Cloud) | Paddle webhooks are verified with HMAC-SHA256 over the timestamp and raw body (`Paddle-Signature`), and rejected when older than 5 minutes. Out-of-order events are ignored. A known subscription never moves to another account, and a second subscription cannot replace one in effect. Sandbox and live use separate accounts and secrets. The checkout page only opens transactions created for the signed-in account; card details never reach HookShield. |

Architecture details: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#security-model). The last full review, done before the first release by a reviewer who had not written the code, covered authentication, authorization, CSRF and CORS, ingest, SSRF, transform rules, crypto, alerts, the all-in-one proxy, billing webhooks and the dashboard. Every finding was fixed with a regression test or is listed below.

## Running it safely (self-hosted)

- Run with `HS_ENV=production` (the default in the Docker images). It turns SSRF protection and `Secure` cookies on. The services warn when `development` runs on a public URL.
- Serve everything over HTTPS. The production compose file and the one-click templates do it for you.
- **Back up `HS_ENCRYPTION_KEY`** (or the generated key file) separately from the database backups. Without it the data cannot be read. See [docs/SELF_HOSTING.md](docs/SELF_HOSTING.md#the-encryption-key).
- Set `HS_TRUSTED_PROXIES` to your reverse proxy only. Trusting a whole private network lets anyone on it fake client IPs and bypass IP allow-lists.
- Keep `HS_ALLOW_PRIVATE_TARGETS=false` unless every user of the install is trusted. Turning it on lets users make the worker call your internal network.
- Configure email (Resend or SMTP). Without it, verification emails are only logged, and unverified accounts cannot use email alerts.
- Use API keys with the smallest scope that works: `read_only` for `hookshield listen`.

## Known limitations

These are accepted trade-offs. Each has a reason, and some have a planned improvement:

- **Registration reveals whether an email has an account** (`auth.email_taken`). This is common practice for usability; login and password reset do not reveal it.
- **Per-email login lockout:** anyone can lock an email's password login for 15 minutes with 10 wrong attempts. The lockout protects the password, and Google sign-in and password reset keep working.
- **HMAC mode has no timestamp window:** a signed request can be replayed by whoever obtains it, for example from the destination's logs. Prefer providers' timestamped signatures, or token mode with token rotation.
- **The dashboard's CSP allows inline scripts** (`'unsafe-inline'`). Next.js streams inline scripts with every page, and nonces would force every page to render dynamically. Scripts from other origins, framing and connections to other hosts stay blocked, and payloads are always rendered as text.
- **An endpoint's legacy `alert_webhook_url`** is returned unmasked to its owner, including `read_only` API keys. Alert channels store their targets encrypted and masked; prefer them.
