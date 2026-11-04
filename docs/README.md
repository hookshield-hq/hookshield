# HookShield documentation

HookShield is an open-source, zero-trust webhook gateway. It sits between webhook senders (Stripe, GitHub, Shopify, your own services) and your application: it authenticates every request at the edge, transforms it, encrypts it with AES-256-GCM, stores it, and delivers it with retries, alerts and a live debugger.

```
Sender ──► [Token check] ──► Processing queue ──► Transform + encrypt + store ──► Dispatch queue ──► Your app
               │ invalid                                                              │ fails N times
               ▼                                                                      ▼
          401 dropped                                                         Retry ► DLQ ► Alert
```

These pages are the same Markdown files you find in the repository's [`docs/`](.) folder; the website renders them.

## Start here

| I want to… | Read |
|---|---|
| Run HookShield on my own server or in one click | [Self-hosting](SELF_HOSTING.md) |
| See every environment variable | [Configuration](CONFIGURATION.md) |
| Receive webhooks on my laptop while I develop | [CLI](CLI.md) |
| Use HookShield Cloud | Sign up at [app.hookshield.dev](https://app.hookshield.dev) — the free plan needs no card |

## Your first webhook

1. **Create an endpoint** in the dashboard (**Endpoints → New endpoint**): give it a name, the URL of your application, and how senders authenticate (token, HMAC signature or IP allow-list).
2. **Copy its webhook URL** — `https://in.hookshield.dev/v1/ep_…` on Cloud, `https://your.domain/in/v1/ep_…` on a self-hosted install — and paste it into the provider's webhook settings.
3. **Send a test event** from the dashboard (**Sandbox**, or **Send test** on the endpoint) or with the CLI: `hookshield trigger ep_… --event invoice.paid`.
4. **Watch it arrive** in the live feed and open it in the debugger: the timeline shows each stage with its latency, the original and transformed request, and every delivery attempt with your application's response.

From there, add [transform rules](TRANSFORM.md), tune the [retry policy](DELIVERY.md#retries) and set up [alerts](ALERTS.md).

## How it works

| Page | What it covers |
|---|---|
| [Receiving webhooks](INGEST.md) | The webhook URL, authentication modes, status codes, limits — the contract for senders |
| [Transform rules](TRANSFORM.md) | Removing, adding, renaming and formatting fields and headers |
| [Delivery and retries](DELIVERY.md) | Headers your application receives, retry policies, dead-letter queue, SSRF protection |
| [Alerts](ALERTS.md) | Slack, Telegram, email and webhook alerts, cooldown and grouping |
| [Architecture](ARCHITECTURE.md) | Services, queues, data flow and the security model |

## Reference

| Page | What it covers |
|---|---|
| [Control API](API.md) | Every REST route of the dashboard API, authentication with API keys, errors |
| [Database](DATABASE.md) | Schema and migrations |
| [Performance](PERFORMANCE.md) | Load test method and results |

## Project

| Page | What it covers |
|---|---|
| [Security](../SECURITY.md) | Reporting a vulnerability, what is protected, known limitations |
| [Development](DEVELOPMENT.md) | Running everything locally, tests, conventions |
| [Contributing](../CONTRIBUTING.md) | Definition of done for every change |

HookShield is MIT licensed. The code is on [GitHub](https://github.com/hookshield-hq/hookshield).
