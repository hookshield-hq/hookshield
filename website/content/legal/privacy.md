---
title: Privacy Policy
updated: 2026-11-04
draft: true
---

# Privacy Policy

This policy explains what personal data HookShield collects, why, and what you can do about it. It covers this website (`hookshield.dev`) and the hosted service **HookShield Cloud** (`app.hookshield.dev`, `api.hookshield.dev`, `in.hookshield.dev`).

**Self-hosted installations are not covered:** if you run the open-source software on your own servers, we receive no data from it. Whoever operates that installation is responsible for it.

## Who we are

HookShield Cloud is operated by **BOREALCODE COMERCIO E SERVIÇOS LTDA** (trading as BorealCode, CNPJ 48.392.038/0001-83), Rua Lucas José de Alvarenga, 1, Jardim Amália, São Paulo – SP, 05892-470, Brazil ("we", "us"), the controller of your personal data under the Brazilian General Data Protection Law (LGPD) and, where it applies, the GDPR. Contact for anything in this policy: **hello@hookshield.dev**.

## This website

The website is a set of static pages. It sets **no cookies**, runs **no analytics or advertising trackers** and loads no third-party scripts. Your browser stores your theme choice locally (it never leaves your device). The hosting provider of the website processes your IP address and browser details to deliver the pages and protect against abuse, as part of normal web server logs.

## HookShield Cloud

### Data we collect to run your account

| Data | Why | Kept |
|---|---|---|
| Name, email address, password (stored only as an argon2id hash), language and theme preferences | To create and secure your account and to communicate with you | While your account exists |
| Google account identifier and email, if you sign in with Google | To let you sign in with Google | While your account exists |
| Sessions: a hashed session token, IP address, browser user agent, timestamps | To keep you signed in and detect abuse | Deleted within an hour after the session ends (sign-out, inactivity, or at most 90 days after sign-in) |
| One-time links (email verification, password reset), stored as hashes | To confirm your email and reset your password | Deleted within an hour after they are used or expire |
| Security records: failed sign-in counters per email and IP address | To block password guessing | Minutes to hours |
| Subscription status, plan, customer and subscription identifiers from our payment provider | To apply your plan and bill usage | While your account exists, then as required by tax law |
| Usage counters (number of requests per month) | To apply plan limits and bill usage | While your account exists |
| Support emails you send us | To answer you | Up to 2 years |

We use your email address for transactional messages: verification, password reset, alerts you configure and important service or billing notices. We do not send marketing emails without your consent.

### Webhooks you route through HookShield

The webhooks that third parties send to your endpoints, their headers, the requests HookShield delivers to your applications and their responses are **your content**. We process them only to provide the service as you configure it (receive, transform, store, deliver, show them in your dashboard, replay them). For this content we act as a processor on your behalf; you are responsible for having a legal basis to send it to us.

This content is encrypted at rest with AES-256-GCM and deleted automatically at the end of your plan's retention period (24 hours on Developer, 30 days on Pro, 90 days on Business, as agreed on Enterprise). Webhook metadata (endpoint, time, size, status, sender IP address, event type) is kept for the same period. Secrets you store are encrypted and are never shown again after you save them.

### Cookies in the dashboard

The dashboard (`app.hookshield.dev`) and the API use only what is needed to work — no analytics or advertising cookies:

| Cookie | Purpose | Lifetime |
|---|---|---|
| `hs_session` | Keeps you signed in (HttpOnly, Secure) | Until you sign out, or 30 days without activity (at most 90 days) |
| `hs_oauth_state` | Protects the Google sign-in against forgery | A few minutes, during sign-in |
| `NEXT_LOCALE` | Remembers the dashboard language | Browser session |

Your theme choice is kept in your browser's local storage and in your account preferences.

### Payments

Payments are handled by **Paddle** (Paddle.com Market Ltd), our merchant of record, under its own [privacy policy](https://www.paddle.com/legal/privacy). Paddle collects the billing details it needs (name, email, country, payment method). We never receive your full card details; we receive your subscription status, plan and identifiers.

### Service providers

| Provider | Purpose | Location |
|---|---|---|
| Hostinger | Servers and database of HookShield Cloud | United States |
| Cloudflare | DNS of hookshield.dev and encrypted database backups (R2) | United States |
| Paddle | Payments, invoices, taxes | United Kingdom |
| Resend | Sending transactional emails | United States |
| Google | Sign-in with Google (only if you use it) | Worldwide |
| GitHub (GitHub Pages) | Serving this website | United States |

Transfers outside your country rely on the safeguards required by law (such as the European Commission's Standard Contractual Clauses).

### Legal bases

We process account data to perform our contract with you; security records and abuse prevention on our legitimate interest in keeping the service safe; billing records to meet legal obligations; and optional communications on your consent. If you are in Brazil, these correspond to the legal bases of the LGPD (Lei 13.709/2018): contract performance, legitimate interest, legal obligation and consent.

## Your rights

You can access, correct, export or delete your personal data, object to or restrict its processing, and withdraw consent at any time. Most account details can be changed in **Settings**. To export or delete your account and everything in it, email **hello@hookshield.dev** from the address of the account; we answer within 30 days. You can also complain to your data protection authority (in Brazil, the ANPD).

## Security

Besides encryption at rest, HookShield hashes passwords, tokens and API keys, protects sessions against CSRF and limits sign-in attempts. The details are in our [security documentation](/docs/security/). No system is perfectly secure; if we ever have a breach that affects your data we will notify you and the authorities as the law requires.

## Children

HookShield is a developer tool and is not directed to children under 16. We do not knowingly collect their data.

## Changes

We will post any change here, update the date above and, for material changes, email account owners before they take effect.
