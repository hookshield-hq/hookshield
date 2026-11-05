# Railway template

Railway templates are created in Railway's dashboard (template composer),
not from a file in the repository. This page is the exact recipe; once the
template is published, put its "Deploy on Railway" button in the main README.

## Services

| Service | Source | Settings |
|---|---|---|
| **HookShield** | Docker image `ghcr.io/hookshield-hq/hookshield:latest` | Public networking on port **8000** (generate a domain). Healthcheck path `/readyz`. Volume mounted at `/data` (keeps the generated key if `HS_ENCRYPTION_KEY` is ever removed) |
| **Postgres** | Railway's Postgres template | — |
| **Redis** | Railway's Redis template | — |

## Variables of the HookShield service

| Variable | Value |
|---|---|
| `HS_DATABASE_URL` | `${{Postgres.DATABASE_URL}}` |
| `HS_REDIS_URL` | `${{Redis.REDIS_URL}}` |
| `HS_ENCRYPTION_KEY` | `${{secret(64, "abcdef0123456789")}}` — 64 hex characters = 32 random bytes |
| `HS_EMAIL_FROM` | empty (user fills: `HookShield <no-reply@their.domain>`) |
| `HS_RESEND_API_KEY` | empty (user fills: `re_…`) |
| `HS_RETENTION` | `720h` |
| `PORT` | `8000` — must match the public networking port (otherwise Railway may inject another `PORT` and the domain answers 502) |
| `RAILWAY_RUN_UID` | `0` — Railway mounts volumes owned by root; the image runs as the `node` user and could not write `/data` otherwise |

`RAILWAY_PUBLIC_DOMAIN` is set by Railway; `hookshield-server` derives the
public URL from it. With a custom domain, set `HS_PUBLIC_URL=https://your.domain`.

Mark `HS_ENCRYPTION_KEY` in the template description: "Back this value up —
without it stored webhooks cannot be decrypted."

## Template details

- Name: HookShield
- Description: Open-source webhook gateway: zero-trust tokens, AES-256 encryption at rest, retries, replay and alerts.
- Category: Other / Developer tools
- Icon: `web/src/assets/only-icon.webp` (exported as PNG)

## Check before publishing

1. Deploy the template in an empty Railway project.
2. Open the generated domain → create an account → create an endpoint → send
   `curl -X POST https://<domain>/in/v1/<slug> -H '<auth header>' -d '{}'` → the
   delivery appears in the logs.
3. Redeploy: the service starts again with the same key (no "key does not match" error).
