import { expect, test } from "@playwright/test";
import { createEndpoint, dialog, register, uniqueEmail, verifyEmail, watchErrors } from "./helpers";

// Phase 8 screens against the real stack (make dev / make up): every test
// signs up a fresh account, so they can run in any order. The demo sink on
// :4000 answers 200, so deliveries succeed.

test("endpoint lifecycle: create, send a test event, inspect it and replay it", async ({ page }) => {
  const errors = watchErrors(page, [404]); // the debugger may ask before the worker stored the webhook
  await register(page, uniqueEmail("ep"));
  await expect(page.getByRole("heading", { name: "Get your first webhook through" })).toBeVisible();

  await createEndpoint(page, "Stripe e2e");
  await expect(page.getByText("http://localhost:8080/v1/ep_").first()).toBeVisible();

  // Send a test event → debugger opens on the new log, which gets delivered.
  await page.getByRole("button", { name: "Send test" }).click();
  await expect(page).toHaveURL(/\/en\/logs\?id=/);
  const drawer = dialog(page);
  await expect(drawer.getByText("Delivered", { exact: true }).first()).toBeVisible({ timeout: 15_000 });
  await expect(drawer.getByText("Delivery attempt 1 (200)")).toBeVisible();
  await expect(drawer.getByText('"hookshield.test"')).toBeVisible();
  await drawer.getByRole("tab", { name: "Destination response (1)" }).click();
  await expect(drawer.getByText("Response body")).toBeVisible();

  // Replay → a new log linked to the original.
  await drawer.getByRole("button", { name: "Replay", exact: true }).click();
  await expect(page.getByText("Replay queued")).toBeVisible();
  await expect(dialog(page).getByText("Replay of")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("row").filter({ hasText: "hookshield.test" })).toHaveCount(2);
  await expect(page.getByRole("row").filter({ hasText: "Replay" })).toHaveCount(1);

  // Filters: no failed webhooks.
  await page.getByRole("button", { name: "Failed" }).click();
  await expect(page.getByText("Nothing matches these filters")).toBeVisible();
  await page.getByRole("button", { name: "Clear filters" }).first().click();
  await expect(page.getByRole("row").filter({ hasText: "hookshield.test" })).toHaveCount(2);

  // The endpoint list shows traffic for the last 24 h.
  await page.goto("/en/endpoints");
  const row = page.getByRole("row").filter({ hasText: "Stripe e2e" });
  await expect(row).toContainText("Active");
  await expect(row).toContainText("100%");
  expect(errors()).toEqual([]);
});

test("endpoint settings: pause, retry policy and delete", async ({ page }) => {
  const errors = watchErrors(page);
  await register(page, uniqueEmail("settings"));
  await createEndpoint(page, "Retry e2e");

  await page.getByRole("button", { name: "Pause" }).click();
  await expect(page.getByText("Endpoint paused")).toBeVisible();
  await expect(page.getByRole("button", { name: "Send test" })).toBeDisabled();
  await page.getByRole("button", { name: "Resume" }).click();
  await expect(page.getByText("Endpoint active again")).toBeVisible();

  // Retry policy: the preview follows the strategy, then it is saved.
  await page.getByRole("tab", { name: "Delivery & retries" }).click();
  await expect(page.getByText("Gives up after 5 min 10 s")).toBeVisible();
  await page.getByRole("radio", { name: /Fixed interval/ }).click();
  await page.getByLabel("Maximum retries").fill("3");
  await expect(page.getByText("Gives up after 30 s")).toBeVisible();
  await page.getByRole("button", { name: "Add header" }).click();
  await page.getByRole("textbox", { name: "Header" }).fill("Authorization");
  await page.getByRole("textbox", { name: "Value" }).fill("Bearer {{secrets.E2E_KEY}}");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByText("Changes saved")).toBeVisible();
  // The missing secret is flagged with a link to create it.
  await expect(page.getByText("Missing secrets: E2E_KEY.")).toBeVisible();

  await page.reload();
  await page.getByRole("tab", { name: "Delivery & retries" }).click();
  await expect(page.getByRole("radio", { name: /Fixed interval/ })).toHaveAttribute("aria-checked", "true");
  await expect(page.getByLabel("Maximum retries")).toHaveValue("3");

  await page.getByRole("button", { name: "Delete endpoint" }).click();
  await dialog(page).getByRole("button", { name: "Delete endpoint" }).click();
  await expect(page).toHaveURL(/\/en\/endpoints$/);
  await expect(page.getByText("Create your first endpoint")).toBeVisible();
  expect(errors()).toEqual([]);
});

test("transformer: rules preview live, save, and apply to deliveries", async ({ page }) => {
  // 422: the preview of an invalid rule; 404: the debugger asks for the test
  // webhook a moment before the worker has stored it, then retries.
  const errors = watchErrors(page, [404, 422]);
  await register(page, uniqueEmail("transform"));
  const { id } = await createEndpoint(page, "Transform e2e");

  await page.goto(`/en/transform?endpoint=${id}`);
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await page.getByRole("textbox", { name: "From", exact: true }).fill("user_id");
  await page.getByRole("textbox", { name: "To", exact: true }).fill("customerId");
  // The output preview is the only code panel (<pre>) on the page.
  await expect(page.locator("pre", { hasText: '"customerId"' })).toBeVisible();
  await expect(page.locator("pre", { hasText: '"user_id"' })).toHaveCount(0);

  // An invalid rule is flagged on the rule itself.
  await page.getByRole("button", { name: "Remove", exact: true }).click();
  await expect(page.getByText("This rule needs a field path.")).toBeVisible();
  await page.getByRole("button", { name: "Remove rule" }).nth(1).click();

  await page.getByRole("button", { name: "Save rules" }).click();
  await expect(page.getByText("Rules saved. New webhooks use them right away.")).toBeVisible();

  // Through the sandbox → the debugger shows the transformed request.
  await page.goto(`/en/sandbox?endpoint=${id}`);
  await page.getByRole("textbox", { name: "Body" }).fill('{"type":"user.created","user_id":7}');
  await page.getByRole("button", { name: "Send test event" }).click();
  await page.getByRole("link", { name: "Open in debugger" }).click();
  const drawer = dialog(page);
  await expect(drawer.getByText("Pipeline transformation")).toBeVisible({ timeout: 15_000 });
  await drawer.getByRole("tab", { name: "Transformed request" }).click();
  await expect(drawer.getByText('"customerId"')).toBeVisible();
  expect(errors()).toEqual([]);
});

test("secrets, API keys and alert channels", async ({ page }) => {
  const errors = watchErrors(page, [403]); // the email channel before the address is confirmed
  const email = await register(page, uniqueEmail("resources"));

  await page.goto("/en/secrets");
  await page.getByRole("button", { name: "New secret" }).first().click();
  await dialog(page).getByLabel("Name").fill("stripe key");
  await expect(dialog(page).getByLabel("Name")).toHaveValue("STRIPE_KEY");
  await dialog(page).getByLabel("Value").fill("sk_test_123");
  await dialog(page).getByRole("button", { name: "Create" }).click();
  await expect(page.getByText("{{secrets.STRIPE_KEY}}")).toBeVisible();
  await expect(page.getByText("sk_test_123")).toHaveCount(0); // values are never shown

  await page.goto("/en/api-keys");
  await page.getByRole("button", { name: "New API key" }).first().click();
  await dialog(page).getByLabel("Name").fill("CI");
  await dialog(page).getByRole("radio", { name: /Ingest/ }).click();
  await dialog(page).getByRole("button", { name: "Create key" }).click();
  const key = await dialog(page).locator("code").textContent();
  expect(key).toMatch(/^hs_live_/);
  await expect(dialog(page).getByRole("button", { name: "Done" })).toBeDisabled(); // copy first
  // Until it is copied, Escape, the backdrop and the close button cannot discard the key.
  await page.keyboard.press("Escape");
  await page.mouse.click(5, 5);
  await expect(dialog(page)).toBeVisible();
  await expect(dialog(page).locator("code")).toHaveText(key!);
  await expect(dialog(page).getByRole("button", { name: "Close" })).toHaveCount(0);
  await dialog(page).getByRole("button", { name: "Copy" }).click();
  await dialog(page).getByRole("button", { name: "Done" }).click();
  const row = page.getByRole("row").filter({ hasText: "CI" });
  await expect(row).toContainText("Ingest");
  await expect(page.getByText(key!)).toHaveCount(0); // masked for good
  await row.getByRole("button", { name: "Revoke" }).click();
  await dialog(page).getByRole("button", { name: "Revoke" }).click();
  await expect(row).toContainText("Revoked");

  // Email alerts need a confirmed address (security review: unverified accounts could mail anyone).
  await page.goto("/en/alerts");
  await page.getByRole("button", { name: "New channel" }).first().click();
  await dialog(page).getByLabel("Email address").fill("ops@example.com");
  await dialog(page).getByRole("button", { name: "Create" }).click();
  await expect(dialog(page).getByText("Verify your email address before sending alerts by email")).toBeVisible();
  await verifyEmail(page, email);
  await page.goto("/en/alerts");
  await page.getByRole("button", { name: "New channel" }).first().click();
  await dialog(page).getByLabel("Email address").fill("ops@example.com");
  await dialog(page).getByRole("button", { name: "Create" }).click();
  const channel = page.getByRole("row").filter({ hasText: "ops@example.com" });
  await expect(channel).toContainText("All endpoints");
  await channel.getByRole("switch").click();
  await expect(channel.getByRole("switch")).toHaveAttribute("aria-checked", "false");
  await channel.getByRole("button", { name: "Delete channel?" }).click();
  await dialog(page).getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("No alert channels")).toBeVisible();
  expect(errors()).toEqual([]);
});

test("account settings: profile and password", async ({ page }) => {
  const errors = watchErrors(page, [422]); // the wrong current password
  await register(page, uniqueEmail("account"), "Ana");
  await page.goto("/en/settings");
  await page.getByLabel("Name").fill("Ana Maria");
  await page.getByRole("button", { name: "Save profile" }).click();
  await expect(page.getByText("Profile saved")).toBeVisible();
  await expect(page.getByRole("button", { name: "Account menu" })).toBeVisible();

  await page.getByLabel("Current password").fill("not my password!!");
  await page.getByLabel("New password").fill("another long passphrase");
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("The current password is wrong.")).toBeVisible();
  await page.getByLabel("Current password").fill("a long enough passphrase");
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("Password changed. Other sessions were signed out.")).toBeVisible();
  expect(errors()).toEqual([]);
});

test("every screen renders in Portuguese and Spanish without missing texts", async ({ page }) => {
  const errors = watchErrors(page);
  await register(page, uniqueEmail("i18n"));
  const { id } = await createEndpoint(page, "i18n");
  const screens = ["", "/endpoints", `/endpoints/${id}`, "/logs", "/transform", "/sandbox", "/alerts", "/secrets", "/api-keys", "/settings"];
  for (const locale of ["pt-BR", "es"]) {
    for (const s of screens) {
      await page.goto(`/${locale}${s}`);
      await expect(page.locator("main h1").filter({ visible: true })).toBeVisible();
      await expect(page.locator("main")).not.toContainText(/\b(endpoints|logs|transform|sandbox|alerts|secrets|apiKeys|settings|dashboard)\.[a-z][a-zA-Z]+/);
    }
  }
  expect(errors()).toEqual([]);
});
