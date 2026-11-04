import { expect, test } from "@playwright/test";
import { dialog, register, uniqueEmail, watchErrors } from "./helpers";

// Billing exists only on HookShield Cloud. Run these against the cloud stack
// with the fake Paddle (hookshield-cloud: scripts/dev.sh); on a
// self-hosted API they check that the billing page stays hidden instead.

test("billing: upgrade through the checkout, switch plans, cancel in the portal", async ({ page, request }) => {
  const errors = watchErrors(page);
  const apiUrl = (await (await request.get("/api/config")).json()).apiUrl as string;
  const billing = (await (await request.get(`${apiUrl}/v1/features`)).json()).billing as boolean;
  await register(page, uniqueEmail("billing"));
  const nav = page.getByRole("navigation", { name: "Main navigation" });

  if (!billing) {
    await expect(nav.getByRole("link", { name: "Billing" })).toHaveCount(0);
    await page.goto("/en/billing");
    await expect(page.getByText("Billing is not available on this server")).toBeVisible();
    expect(errors()).toEqual([]);
    return;
  }

  await nav.getByRole("link", { name: "Billing" }).click();
  await expect(page.getByRole("heading", { name: "Plans & billing", level: 1 })).toBeVisible();
  await expect(page.getByText("of 5,000 included")).toBeVisible();
  await expect(page.getByRole("button", { name: "Current plan" })).toBeVisible();

  // Checkout on the provider, back with ?checkout=success, plan activated by the webhook.
  await page.getByRole("button", { name: "Upgrade to Pro" }).click();
  await expect(page).toHaveURL(/\/en\/billing$/, { timeout: 15_000 });
  await expect(page.getByText("Your plan is active")).toBeVisible();
  await expect(page.getByText("Active", { exact: true }).first()).toBeVisible();
  await expect(page.getByText(/^Renews on /)).toBeVisible();
  await expect(page.getByText("of 100,000 included")).toBeVisible();

  // Switching is applied immediately (prorated).
  await page.getByRole("button", { name: "Switch to Business" }).click();
  await dialog(page).getByRole("button", { name: "Switch plan" }).click();
  await expect(page.getByText("You are now on Business")).toBeVisible();
  await expect(page.getByText("of 1,000,000 included")).toBeVisible();

  // The customer portal (fake) cancels; the plan stays until the end of the period.
  await page.getByRole("button", { name: "Manage billing" }).click();
  await expect(page.getByRole("heading", { name: /^Subscription / })).toBeVisible();
  await page.getByRole("button", { name: "Cancel subscription" }).click();
  await page.goto("/en/billing");
  await expect(page.getByText("Cancelled", { exact: true })).toBeVisible();
  await expect(page.getByText(/^Cancelled: Business stays active until /)).toBeVisible();
  expect(errors()).toEqual([]);
});

test("billing page in Portuguese and Spanish", async ({ page, request }) => {
  const apiUrl = (await (await request.get("/api/config")).json()).apiUrl as string;
  test.skip(!(await (await request.get(`${apiUrl}/v1/features`)).json()).billing, "self-hosted API: no billing");
  const errors = watchErrors(page);
  await register(page, uniqueEmail("billing-i18n"));
  for (const [locale, heading, free] of [
    ["pt-BR", "Planos e cobrança", "Grátis"],
    ["es", "Planes y facturación", "Gratis"],
  ]) {
    await page.goto(`/${locale}/billing`);
    await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible();
    await expect(page.getByText(free, { exact: true })).toBeVisible();
  }
  expect(errors()).toEqual([]);
});
