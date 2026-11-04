# Self-hosting HookShield

HookShield runs anywhere Docker runs. Self-hosted installs have every feature and no plan limits.

| Option | Best for | What runs |
|---|---|---|
| [Docker Compose on your server](#docker-compose-on-your-server) | A VPS (Hetzner, DigitalOcean…), full control, scaling workers | Caddy (HTTPS) + api, ingest, worker, web + PostgreSQL + Redis |
| [Render](#render) | One click, managed database | All-in-one image + Render Postgres + Render Key Value |
| [Railway](#railway) | One click, managed database | All-in-one image + Railway Postgres + Redis |
| [Coolify](#coolify) | Your own server with a PaaS UI | All-in-one image + PostgreSQL + Redis |

Every option ends at one URL:

| Path | Serves |
|---|---|
| `https://your.domain/` | Dashboard |
| `https://your.domain/v1/…` | API ([API.md](API.md)) |
| `https://your.domain/in/v1/{slug}` | Ingest: the URLs you give to webhook senders ([INGEST.md](INGEST.md)) |

On the first start the services apply the database migrations and, if you did not set `HS_ENCRYPTION_KEY`, generate the encryption key. Then open the URL and create your account.

## Images

Published to GitHub Container Registry for `linux/amd64` and `linux/arm64` by `.github/workflows/release.yml`:

| Image | Contents |
|---|---|
| `ghcr.io/hookshield-hq/hookshield` | **All-in-one**: `hookshield-server` runs the API, ingest, worker and dashboard in one container on one port (8000). Used by the one-click deploys. |
| `ghcr.io/hookshield-hq/hookshield-backend` | `api`, `ingest`, `worker`, `hookshield-server` and the `hookshield` CLI; pick the process with the command. |
| `ghcr.io/hookshield-hq/hookshield-web` | The dashboard (Next.js). |

Tags: `latest` (latest release), `1.2.3` and `1.2` (releases), `edge` (main branch), `sha-<commit>`.

## The encryption key

Webhook bodies and headers, endpoint secrets and alert targets are encrypted with one AES-256 key. **Without the key, that data cannot be read — not by you, not by us.**

- Set it yourself with `HS_ENCRYPTION_KEY` (`openssl rand -base64 32`), or let the first start generate it: into the `keys` volume (`/var/lib/hookshield/encryption.key`) with Docker Compose, `/data/encryption.key` in the all-in-one image. The one-click templates have the platform generate it as a variable.
- **Back it up once**, away from the database backups (a password manager is fine):
  ```bash
  docker compose exec api cat /var/lib/hookshield/encryption.key     # Docker Compose
  ```
  On Render, Railway and Coolify, copy `HS_ENCRYPTION_KEY` from the service's environment variables.
- The database stores a value encrypted with the key. A service started with a different key refuses to start (`encryption key does not match this database`), so a wrong or regenerated key never mixes data. If the database has data and the key file is missing, the services do not create a new key: they stop and ask you to restore it.
- Key formats: base64 of 32 bytes, or 64 hex characters.

## Docker Compose on your server

Requirements: a Linux server with Docker (Compose v2), ports 80 and 443 open, and a DNS `A`/`AAAA` record for your domain pointing to it. 2 GB of RAM is a comfortable start.

```bash
# on the server
git clone --depth 1 https://github.com/hookshield-hq/hookshield /opt/hookshield
cd /opt/hookshield/deploy
./setup.sh hooks.example.com    # writes .env: domain + random database password
docker compose up -d            # Caddy gets the certificate; the first start creates the key
docker compose logs -f api      # "generated a new encryption key — BACK IT UP NOW"
```

Open `https://hooks.example.com` and create your account. Then:

1. **Email** — set `HS_RESEND_API_KEY` and `HS_EMAIL_FROM` in `.env` (or `HS_SMTP_URL`) and run `docker compose up -d`. Without it, verification emails and email alerts are only written to the logs.
2. **Back up the key** (above) and **schedule database backups** (below).
3. Optional: Google sign-in (`GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET`, redirect URI `https://hooks.example.com/v1/auth/google/callback`).

Files in `deploy/`:

| File | Purpose |
|---|---|
| `docker-compose.yml` | Caddy, api, ingest, worker, web, PostgreSQL 17, Redis 7 (AOF, `noeviction`, `maxmemory`); memory limit per container, logs rotated (20 MB × 5) |
| `Caddyfile` | HTTPS and path routing (`/v1` → api, `/in` → ingest, the rest → web) |
| `setup.sh` | Creates `.env` (refuses to overwrite one) |
| `backup.sh` / `restore.sh` | Database dumps and restore |

Every `HS_*` variable in `.env` reaches the backend services ([CONFIGURATION.md](CONFIGURATION.md)).

**Scaling.** Workers are stateless consumers of the Redis queues (consumer groups): `docker compose up -d --scale worker=3`.

**Memory.** Each container has a memory limit (PostgreSQL 1 GB, Redis 512 MB, Caddy 256 MB, the others 512 MB; limits, not reservations). Redis gets `--maxmemory 256mb` and never evicts keys: when it is full, new webhooks are refused (senders retry) instead of queued ones being lost. With a long backlog to absorb, raise it in `.env`: `HS_REDIS_MAXMEMORY=1gb` together with `HS_REDIS_MEM_LIMIT=2g` (about twice `maxmemory`, room for AOF rewrites), then `docker compose up -d`.

**Updates.** Pin a release in `.env` (`HS_VERSION=1.2.0`) or keep `latest`, then:

```bash
docker compose pull && docker compose up -d   # migrations run automatically on start
```

Migrations only move forward; take a backup before upgrading.

### Backups

```bash
./backup.sh                      # → deploy/backups/hookshield-<UTC time>.dump (keeps 14 days)
HS_BACKUP_KEEP_DAYS=30 ./backup.sh /mnt/offsite
```

Nightly with cron (`crontab -e`):

```
0 3 * * * cd /opt/hookshield/deploy && ./backup.sh >> backups/backup.log 2>&1
```

Copy the dumps off the server (object storage, another machine). Restore into a running stack (replaces all data; same encryption key required):

```bash
./restore.sh backups/hookshield-20261103T030000Z.dump
```

The restore runs in a single transaction: if it fails, the database is left as it was and the stopped services are started again. On success it also drops the ingest's endpoint cache in Redis (`<HS_REDIS_PREFIX>cache:ep:*`).

Redis holds queues in flight (webhooks accepted but not yet processed, scheduled retries). It is persisted to its volume every second; it does not need separate backups.

## Render

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/hookshield-hq/hookshield)

The button reads [`render.yaml`](../render.yaml): one web service (all-in-one image), Render Postgres and Render Key Value (`noeviction`). Render generates `HS_ENCRYPTION_KEY`; it asks for `HS_EMAIL_FROM` and `HS_RESEND_API_KEY` (both can stay empty at first).

- The service URL (`https://hookshield-xxxx.onrender.com`) is detected automatically. With a custom domain, add `HS_PUBLIC_URL=https://your.domain`.
- Use a paid instance: free instances sleep when idle and webhook senders would time out while they wake up. Free Render Postgres databases expire.
- Use a **paid Key Value** instance (persistence mode *Journal + Snapshot*, the default for paid instances) in production. Free Key Value instances keep data in memory only: when Render restarts them, webhooks that were accepted but not yet processed, and scheduled retries, are lost. Upgrading a free instance later also loses its data, so pick the paid type when you deploy.
- Update: **Manual Deploy → Deploy latest reference** pulls the newest image.

## Railway

The Railway template (recipe in [`deploy/railway/README.md`](../deploy/railway/README.md)) creates the all-in-one service, Postgres and Redis, and generates the key. The public URL comes from `RAILWAY_PUBLIC_DOMAIN`; with a custom domain set `HS_PUBLIC_URL`.

Without the template: new project → **Deploy from Docker image** `ghcr.io/hookshield-hq/hookshield:latest`, add Postgres and Redis, and set `HS_DATABASE_URL=${{Postgres.DATABASE_URL}}`, `HS_REDIS_URL=${{Redis.REDIS_URL}}`, `HS_ENCRYPTION_KEY` (`openssl rand -base64 32`). Expose port 8000.

## Coolify

**New resource → Docker Compose Empty**, paste [`deploy/coolify/docker-compose.yml`](../deploy/coolify/docker-compose.yml) and deploy. Coolify generates the domain (`SERVICE_URL_HOOKSHIELD`), the database password and the encryption key (`SERVICE_REALBASE64_32_ENCRYPTION`; copy it to your password manager). Set `HS_EMAIL_FROM` and `HS_RESEND_API_KEY` under Environment Variables.

## Running the all-in-one image anywhere

```bash
docker run -d --name hookshield -p 8000:8000 \
  -e HS_DATABASE_URL='postgres://user:pass@db-host:5432/hookshield' \
  -e HS_REDIS_URL='redis://redis-host:6379/0' \
  -e HS_PUBLIC_URL='https://hooks.example.com' \
  -v hookshield-data:/data \
  ghcr.io/hookshield-hq/hookshield:latest
```

Put an HTTPS proxy in front (it must pass the whole path through). The server trusts `X-Forwarded-For` only from loopback by default (on Render and Railway also from their private load balancers); behind your own proxy, set `HS_TRUSTED_PROXIES` to its address — trusting whole private networks would let any neighbour on them spoof client IPs and bypass IP allow-lists. `GET /healthz` is liveness; `GET /readyz` checks the API, ingest, worker and dashboard. Redis must not evict keys (`maxmemory-policy noeviction`): the queues live there.

## Troubleshooting

| Symptom | Cause |
|---|---|
| `encryption key does not match this database` | The service has another key than the one the database was set up with. Use the original key (all services must share it). |
| `no encryption key: … already holds data encrypted with a key` | The key file is gone (volume removed) but the database is not. Restore the key file or set `HS_ENCRYPTION_KEY`. |
| Login works but the dashboard shows network errors | `HS_PUBLIC_URL` does not match the address in the browser (CORS/CSRF check). Set it to the exact origin, scheme included. |
| Webhook senders get `502 server.starting` | The all-in-one server is still starting (migrations) or a part crashed — check the logs; the container restarts on failure. |
| Emails never arrive | No `HS_RESEND_API_KEY`/`HS_SMTP_URL`, or the sender domain is not verified in Resend. The api logs every email it could not send. |
