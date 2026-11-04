import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { expect, request, test, type APIRequestContext, type Page } from "@playwright/test";
import { dialog, register } from "./helpers";

// Screenshots of the dashboard for the public website (website/public/screens),
// in both themes and the three languages, with realistic traffic. Runs only
// with LANDING_SHOTS=1 against the dev stack (make dev-up && make dev, plus this
// app on :3000). The traffic is spread over LANDING_SHOTS_MINUTES (default 20)
// so the one-hour chart has a shape. Tip: start `make dev` with
// HS_PUBLIC_INGEST_URL=https://in.hookshield.dev in .env so the webhook URLs
// look like Cloud's.
//
//   LANDING_SHOTS=1 npx playwright test landing-shots
test.skip(!process.env.LANDING_SHOTS, "set LANDING_SHOTS=1 to capture the website screenshots");
const MINUTES = Number(process.env.LANDING_SHOTS_MINUTES ?? 20);
test.setTimeout((MINUTES + 10) * 60_000);

const API = process.env.HS_API_URL ?? "http://localhost:8081";
const INGEST = process.env.HS_INGEST_URL ?? "http://localhost:8080";
const SINK = process.env.HS_SINK_URL ?? "http://localhost:4000";
const OUT = join(__dirname, "../../../website/public/screens/");
const locales = ["en", "pt-BR", "es"] as const;
const themes = ["dark", "light"] as const;

type Endpoint = { id: string; slug: string; token: string };

/** Writes headers the API's CSRF check expects from the dashboard. */
const write = { Origin: process.env.WEB_URL ?? "http://localhost:3000" };

async function createEndpoint(page: Page, body: Record<string, unknown>): Promise<Endpoint> {
  const res = await page.request.post(`${API}/v1/endpoints`, { data: body, headers: write });
  expect(res.status(), await res.text()).toBe(201);
  const json = (await res.json()) as { endpoint: { id: string; slug: string }; secrets: { token?: string } };
  return { id: json.endpoint.id, slug: json.endpoint.slug, token: json.secrets.token! };
}

// Webhooks are sent from a separate client: the browser's cookies must not
// travel with them (they would show up in the debugger).
let sender: APIRequestContext;

async function send(_page: Page, ep: Endpoint, body: unknown, headers: Record<string, string> = {}) {
  const res = await sender.post(`${INGEST}/v1/${ep.slug}`, {
    data: body,
    headers: { "X-HookShield-Token": ep.token, "Content-Type": "application/json", ...headers },
  });
  expect(res.status()).toBe(202);
}

/** Encodes a PNG screenshot as WebP in the browser (no image tooling needed). */
async function saveWebP(page: Page, png: Buffer, file: string) {
  const dataUrl = await page.evaluate(async (b64) => {
    const img = new Image();
    img.src = `data:image/png;base64,${b64}`;
    await img.decode();
    const canvas = document.createElement("canvas");
    canvas.width = img.width;
    canvas.height = img.height;
    canvas.getContext("2d")!.drawImage(img, 0, 0);
    return canvas.toDataURL("image/webp", 0.82);
  }, png.toString("base64"));
  writeFileSync(`${OUT}${file}`, Buffer.from(dataUrl.split(",")[1], "base64"));
}

const rand = (n: number) => Math.floor(Math.random() * n);
const hex = (n: number) => Array.from({ length: n }, () => "0123456789abcdef"[rand(16)]).join("");

test("website screenshots", async ({ page, browser }) => {
  mkdirSync(OUT, { recursive: true });
  sender = await request.newContext();
  await page.setViewportSize({ width: 1440, height: 900 });
  await register(page, `landing-${Date.now()}@example.com`, "Ada");

  // ── Data ──────────────────────────────────────────────
  const secret = await page.request.put(`${API}/v1/secrets/BILLING_API_KEY`, { data: { value: "sk_live_demo" }, headers: write });
  expect(secret.ok()).toBe(true);
  const stripe = await createEndpoint(page, {
    name: "Stripe payments",
    description: "Invoices and charges",
    target_url: `${SINK}/billing/stripe`,
    outbound_headers: { Authorization: "Bearer {{secrets.BILLING_API_KEY}}" },
    transform_rules: {
      version: 1,
      body: [
        { op: "remove", path: "card" },
        { op: "rename", from: "user_id", to: "customerId" },
        { op: "copy", from: "data.object.id", to: "invoiceId" },
        { op: "set", path: "source", value: "stripe" },
        { op: "set_timestamp", path: "forwardedAt" },
      ],
      headers: [{ op: "set", name: "X-Source", value: "hookshield" }],
    },
  });
  const github = await createEndpoint(page, { name: "GitHub CI", description: "Pushes and workflow runs", target_url: `${SINK}/ci/github` });
  const shopify = await createEndpoint(page, {
    name: "Shopify orders",
    description: "Order fulfilment service",
    target_url: "http://localhost:4999/orders", // nothing listens: deliveries fail and retry
    retry: { strategy: "fixed", max_retries: 2, initial_interval_ms: 2000 },
  });
  const linear = await createEndpoint(page, { name: "Linear issues", description: "Issue sync", target_url: `${SINK}/linear` });
  const clerk = await createEndpoint(page, { name: "Clerk users", description: "Sign-ups and profile changes", target_url: `${SINK}/auth/clerk` });
  const resend = await createEndpoint(page, { name: "Resend events", description: "Email delivery status", target_url: `${SINK}/email/resend` });

  const stripeEvents = ["invoice.paid", "customer.created", "charge.succeeded", "payment_intent.succeeded", "invoice.payment_failed"];
  const githubEvents = ["push", "pull_request", "workflow_run", "check_suite"];
  const sendOne = async () => {
    const r = rand(10);
    if (r < 5) {
      const type = stripeEvents[rand(stripeEvents.length)];
      await send(
        page,
        stripe,
        { id: `evt_${hex(14)}`, type, user_id: 1000 + rand(900), card: { brand: "visa", last4: "4242" }, data: { object: { id: `in_${hex(10)}`, amount_paid: 900 + rand(9000), currency: "usd" } } },
        { "Stripe-Signature": `t=${Math.floor(Date.now() / 1000)},v1=${hex(64)}`, "User-Agent": "Stripe/1.0 (+https://stripe.com/docs/webhooks)" },
      );
    } else if (r < 8) {
      const event = githubEvents[rand(githubEvents.length)];
      await send(page, github, { action: "completed", repository: { full_name: "acme/api" }, sender: { login: "ada" } }, { "X-GitHub-Event": event, "X-GitHub-Delivery": crypto.randomUUID(), "User-Agent": "GitHub-Hookshot/7f3e2a1" });
    } else if (r < 9) {
      const pick = rand(3);
      if (pick === 0) await send(page, linear, { action: "create", type: "Issue", data: { title: "Webhook retries", priority: 2 } }, { "Linear-Event": "Issue" });
      else if (pick === 1) await send(page, clerk, { type: ["user.created", "user.updated", "session.created"][rand(3)], data: { id: `user_${hex(12)}` } });
      else await send(page, resend, { type: ["email.delivered", "email.opened", "email.bounced"][rand(3)], data: { email_id: crypto.randomUUID() } });
    } else {
      await send(page, shopify, { id: 5_000_000 + rand(99999), total_price: "49.00", currency: "USD" }, { "X-Shopify-Topic": "orders/create" });
    }
  };
  // MINUTES of traffic with a varying rate, so the 1h chart shows a curve.
  for (let minute = 0; minute < MINUTES; minute++) {
    const perMinute = Math.round(18 + 10 * Math.sin(minute / 2.5) + rand(6));
    for (let i = 0; i < perMinute; i++) {
      await sendOne();
      await page.waitForTimeout(60_000 / perMinute);
    }
  }
  await page.waitForTimeout(8_000); // last retries and deliveries

  // ── Screens ───────────────────────────────────────────
  for (const theme of themes) {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1.5, storageState: await page.context().storageState() });
    const p = await ctx.newPage();
    await p.addInitScript((t) => localStorage.setItem("theme", t), theme);
    for (const locale of locales) {
      const shot = async (name: string) => {
        await p.waitForTimeout(800); // charts, live feed, preview
        await saveWebP(p, await p.screenshot(), `${name}-${theme}-${locale}.webp`);
      };

      await p.goto(`/${locale}`);
      await expect(p.locator("main h1").filter({ visible: true })).toBeVisible();
      await p.getByRole("radio", { name: "1h" }).filter({ visible: true }).click();
      await shot("dashboard");

      await p.goto(`/${locale}/endpoints`);
      await expect(p.locator("main h1").filter({ visible: true })).toBeVisible();
      await shot("endpoints");

      await p.goto(`/${locale}/transform?endpoint=${stripe.id}`);
      await expect(p.locator("main h1").filter({ visible: true })).toBeVisible();
      await shot("transform");

      await p.goto(`/${locale}/logs`);
      await p.getByRole("row").filter({ hasText: "invoice.paid" }).first().click();
      await expect(dialog(p).getByRole("tab").first()).toBeVisible();
      await shot("debugger");
    }
    await ctx.close();
  }
});
