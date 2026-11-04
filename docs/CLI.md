# hookshield CLI

Receive your HookShield webhooks on `localhost` while you build: no tunnel, no public URL for your laptop. The CLI follows your account's live feed and forwards each webhook to your local app, exactly as HookShield would deliver it.

```
$ hookshield listen ep_k3j7x2m4q5ab --forward localhost:3000/webhooks
Ready! Forwarding webhooks of Stripe (ep_k3j7x2m4q5ab) → http://localhost:3000/webhooks
  Webhook URL  https://hooks.example.com/in/v1/ep_k3j7x2m4q5ab
  HookShield still delivers them to each endpoint's destination too. Ctrl+C to stop.

20:34:12  --> invoice.paid [ep_k3j7x2m4q5ab 01a1a3c3…71d5]  <-- 200 OK (12 ms)
20:34:15  --> customer.created [ep_k3j7x2m4q5ab 01a1a3c3…f29b]  <-- 500 Internal Server Error (3 ms)
```

## Install

```bash
# Linux and macOS (amd64, arm64) — verifies the SHA-256 checksum before installing
curl -fsSL https://raw.githubusercontent.com/hookshield-hq/hookshield/main/scripts/install.sh | sh

# With Go 1.24+
go install github.com/hookshield-hq/hookshield/backend/cmd/hookshield@latest
```

Windows: download `hookshield_<version>_windows_amd64.zip` from the [releases](https://github.com/hookshield-hq/hookshield/releases). Installer options: `HOOKSHIELD_VERSION=1.2.0` (a specific release), `HOOKSHIELD_INSTALL_DIR=~/bin` (default `/usr/local/bin` when writable, else `~/.local/bin`).

## Log in

1. In the dashboard, **API keys → New key**. Scope `read_only` is enough for `listen` and `endpoints`; `trigger` needs `ingest` (or `admin`).
2. Run:

```bash
hookshield login                                   # asks for the API URL and the key (hidden)
hookshield login --api-url https://hooks.example.com --api-key hs_live_…
echo "$HOOKSHIELD_KEY" | hookshield login --api-url https://hooks.example.com   # scripts
```

The API URL is your install's URL (self-hosted: the same address as the dashboard) or HookShield Cloud's (`https://api.hookshield.dev`, the default). `localhost:8081` means `http://`; any other host without a scheme means `https://`.

The login is saved to `~/.config/hookshield/config.json` (Linux), `~/Library/Application Support/hookshield/config.json` (macOS) or `%AppData%\hookshield\config.json` (Windows), readable only by you. `hookshield logout` deletes it — revoke the key in the dashboard to disable it everywhere.

Environment variables override the saved login (CI, containers): `HOOKSHIELD_API_URL`, `HOOKSHIELD_API_KEY`, `HOOKSHIELD_CONFIG` (another config file).

## Commands

| Command | What it does |
|---|---|
| `hookshield login` | Save the API URL and an API key (checked against the API first) |
| `hookshield whoami` | Account, plan, API URL and which key is in use |
| `hookshield endpoints` | Your endpoints: name, slug, status, webhook URL |
| `hookshield listen [endpoint] --forward URL` | Forward webhooks to your local app (below) |
| `hookshield trigger <endpoint>` | Send a test event through the whole pipeline (below) |
| `hookshield logout` | Forget the saved key |
| `hookshield sink` | A local destination that prints every request it receives (`--addr :4000`, `--status 500`, `--fail-first 2`) — handy as `--forward` target or endpoint destination |
| `hookshield version` | CLI version |

Endpoints are given by slug (`ep_…`), ID or exact name.

### `listen`

```bash
hookshield listen ep_k3j7x2m4q5ab --forward localhost:3000/webhooks
hookshield listen Stripe -f 3000                 # port shorthand → http://localhost:3000
hookshield listen --forward 3000/hooks           # every endpoint of the account
```

| Flag | Default | Meaning |
|---|---|---|
| `--forward`, `-f` | — (required) | `3000`, `:3000`, `3000/path`, `localhost:3000/path` or a full `http(s)://` URL; its query string is kept and the webhook's query is appended |
| `--endpoint` | all endpoints | Also accepted as the first argument |
| `--original` | off | Forward the request as received, before the endpoint's transform rules |
| `--print-body` | off | Print your app's response body (first 2 KB) |
| `--skip-verify` | off | Accept a self-signed certificate of your local HTTPS app |
| `--no-color` | colors on a terminal | Also `NO_COLOR=1` |

What your app receives — the same request HookShield's dispatcher sends:

- the method, query, headers and body of the webhook **after transform rules** (`--original`: as received). The body is forwarded **byte for byte**, so provider signatures such as `Stripe-Signature` still verify when no rule changes the body;
- the endpoint's static outbound headers. Headers that use `{{secrets.NAME}}` are **not** sent: secrets never leave the server (the CLI prints a note once);
- `X-HookShield-Id`, `X-HookShield-Event`, `X-HookShield-Timestamp`, `X-HookShield-Forwarded-By: hookshield-cli`, `User-Agent: HookShield-CLI/<version>`.

How it works and what to expect:

- The CLI reads the API's live feed (`GET /v1/live`, Server-Sent Events) and, for each new webhook, the stored request (`GET /v1/logs/{id}?raw=true`). Only webhooks received after `listen` started are forwarded.
- **Your endpoint keeps working normally**: HookShield still delivers every webhook to the endpoint's destination, with retries. The CLI is an extra copy for your machine. If you only want that copy, point the destination at a harmless URL while developing (a paused endpoint refuses webhooks, so they would not reach the CLI either).
- Your app's answers are shown, not stored: they do not count as delivery attempts and nothing is retried. Replays from the dashboard and `trigger` events are forwarded too.
- If the connection drops (network, API restart), the CLI reconnects with backoff (1 s … 30 s) and forwards the webhooks that arrived meanwhile, in order, without duplicates.
- Webhooks are forwarded one at a time, in arrival order; each local request times out after 30 s. Redirects are shown, not followed.

### `trigger`

```bash
hookshield trigger ep_k3j7x2m4q5ab                                   # sample event
hookshield trigger Stripe --event invoice.paid                       # {"type": "invoice.paid", …}
hookshield trigger Stripe --data '{"type":"invoice.paid","amount":10}'
hookshield trigger Stripe --data @fixtures/invoice.json -H 'Stripe-Signature: t=1,v1=test'
cat event.json | hookshield trigger Stripe --data -
```

The event runs through the whole pipeline (transform rules, delivery to the destination, retries, alerts) and shows up in `listen`, the dashboard and the logs. Only the inbound authentication is skipped, because the API key already identifies you. It counts towards the rate limit and the monthly quota. `--header`/`-H` (repeatable) replaces the default headers (`Content-Type: application/json` is kept unless you set one); `--method` is `POST`, `PUT` or `PATCH`. Same as `POST /v1/endpoints/{id}/test` ([API.md](API.md)).

## Exit codes and errors

`0` success · `1` the command failed (message on stderr, starting with `✗`) · `2` wrong usage.

| Message | Fix |
|---|---|
| `not logged in` | `hookshield login`, or set `HOOKSHIELD_API_KEY` |
| `the API key is invalid or was revoked` | Create a new key and log in again |
| `this API key's scope does not allow that` | `trigger` needs a key with scope `ingest` or `admin` |
| `… answered 404: is this the HookShield API URL?` | Use the URL of your install (or the Cloud API) in `--api-url` |
| `connection refused — is your app running on localhost:3000?` | Start your app, or fix `--forward` |

## Releases

Every `v*` tag publishes the CLI for Linux, macOS and Windows (amd64 + arm64) with [GoReleaser](../.goreleaser.yaml) (`.github/workflows/release-cli.yml`): `hookshield_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows) and `checksums.txt`. Try a release build locally with `goreleaser release --snapshot --clean`.
