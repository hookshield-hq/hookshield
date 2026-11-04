# Alerts

HookShield tells you when webhook deliveries keep failing — by webhook, Slack, Telegram or email — in your account's language (English, Português, Español).

## When an alert is sent

For each webhook, **one** alert is raised when either happens first:

- its failed attempts reach the endpoint's `alert_threshold_failures` (default **4**) — "deliveries are failing", HookShield keeps retrying;
- delivery is abandoned (no retries left) — "delivery abandoned".

### Anti-spam: one alert per endpoint per cooldown

A broken destination usually fails *every* webhook. To avoid flooding you, alerts are grouped per endpoint: after an alert, the endpoint is silent for `HS_ALERT_COOLDOWN` (default **10 minutes**). Alerts raised meanwhile are counted, and the next alert says how many were grouped (`suppressed` in the JSON payload, "N more webhooks of this endpoint failed since the previous alert" in messages). Different endpoints have independent cooldowns.

## Channels

Channels belong to the account. A channel can apply to **every endpoint** or to **one endpoint**; disabled channels are ignored. Each endpoint can also have a plain `alert_webhook_url` (from the original data model) that behaves like a webhook channel.

Channel destinations are stored **encrypted** (AES-256-GCM, bound to the channel ID) because they are secrets: a Slack webhook URL or a Telegram bot token gives full posting rights.

| Type | Target (JSON, stored encrypted) | What is sent |
|---|---|---|
| `webhook` | `{"url": "https://ops.example.com/hooks/hookshield"}` | `POST` JSON payload (below) with `X-HookShield-Alert: 1` |
| `slack` | `{"webhook_url": "https://hooks.slack.com/services/…"}` | Slack message with title and details (`<`, `>`, `&` escaped: text from senders cannot become links or mentions) |
| `telegram` | `{"bot_token": "123456:ABC…", "chat_id": "-1001234567890"}` | Bot message (`sendMessage`) |
| `email` | `{"to": "ops@example.com"}` — empty `to` = the account's email | Plain-text email (Resend, or SMTP in development) |

### Setting up each channel

Channels are managed with the API (`/v1/alert-channels`, see [API.md](API.md#alert-channels)) and in the dashboard (**Alerts**).

- **Slack:** create an app → *Incoming Webhooks* → *Add New Webhook to Workspace*, pick the channel, copy the URL.
- **Telegram:** talk to [@BotFather](https://t.me/BotFather) → `/newbot` to get the token; add the bot to your group, send a message, and read the `chat.id` from `https://api.telegram.org/bot<token>/getUpdates` (group IDs start with `-100`).
- **Email:** the server needs `HS_RESEND_API_KEY` (production) or `HS_SMTP_URL` (development, Mailpit), plus `HS_EMAIL_FROM` (see [CONFIGURATION.md](CONFIGURATION.md)). Without either, email channels are skipped and the worker logs why. Creating an email channel needs a **verified account email** (`alert.email_unverified`), and an account sends at most **100 alert emails an hour** (the rest are skipped and logged): otherwise an unverified account could use alerts to mail anyone from your domain.
- **Webhook:** any HTTPS endpoint answering `2xx`. The same SSRF rules as deliveries apply ([DELIVERY.md](DELIVERY.md#security-destination-restrictions-ssrf)).

### Webhook payload

```json
{
  "type": "hookshield.alert",
  "final": false,
  "endpoint": {"id": "fad66192-…", "name": "Stripe production", "slug": "ep_stripe"},
  "webhook_id": "01a1a280-cb74-755c-b0fa-0334df681055",
  "event_type": "payment.failed",
  "attempts": 4,
  "last_status": 503,
  "last_error": "",
  "suppressed": 2,
  "at": "2026-11-03T17:41:52.123Z",
  "url": "https://app.hookshield.dev/logs/01a1a280-cb74-755c-b0fa-0334df681055",
  "message": "⚠️ Webhook deliveries are failing — Stripe production"
}
```

`last_status` is set when the destination answered; otherwise `last_error` holds the failure class (`timeout`, `connection_refused`, `dns`, `tls`, …). `url` opens the webhook in the dashboard (`HS_APP_URL`).

### Message example (email, Slack, Telegram)

```
❌ Entrega de webhook abandonada — Stripe production

Evento: payment.failed
Tentativas com falha: 6
Último resultado: HTTP 503
ID do webhook: 01a1a280-cb74-755c-b0fa-0334df681055
Não haverá novas tentativas. Você pode reenviá-lo pelo painel.
Abrir no HookShield: https://app.hookshield.dev/logs/01a1…
```

## Reliability

- The dispatcher only **enqueues** alerts (`hs:stream:alerts`); a separate sender in the worker talks to the channels, so a slow Slack never delays webhook deliveries.
- Each channel is tried up to 3 times (waits of 1 s and 5 s). One failing channel never prevents the others from being notified.
- Alerts survive worker restarts (Redis stream + consumer group; unacknowledged alerts are reclaimed).
- Channel URLs and tokens never appear in logs: request errors are reported without the URL.

## Try it locally

Both local stacks include **Mailpit**, which catches every email (web UI on http://localhost:8025). `make seed` creates an account-wide email channel and an endpoint `ep_failing` whose destination always refuses connections, with no retries and an alert after the first failure:

```bash
make up && make demo-alert          # Docker
make dev-up && make dev             # native, then in another terminal:
make smoke-alert
```

```
✓ alert email received: ❌ Webhook delivery abandoned — Demo failing endpoint
  open http://localhost:8025 to read it
```

Change the demo account's language to see the translations: `make psql` → `UPDATE users SET locale = 'pt-BR';` (or `es`).

## Code

`backend/internal/alert`: `Service` (cooldown + queue), `Sender` (channels, retries), `Render` (localized messages — every key must exist in all three languages, enforced by tests). Email transport lives in `internal/mailer` (`ResendMailer`, `SMTPMailer`).
