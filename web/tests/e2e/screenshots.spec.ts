import { expect, test } from "@playwright/test";
import { createEndpoint, dialog } from "./helpers";

// Visual check of every screen in every language and theme (guide D7: labels
// verified visually in en, pt-BR and es; Spanish/Portuguese run ~30% longer).
// Runs only with SCREENSHOTS=1; images go to test-results/screens/.
test.skip(!process.env.SCREENSHOTS, "set SCREENSHOTS=1 to capture screenshots");
test.setTimeout(240_000);

const locales = ["en", "pt-BR", "es"] as const;
const screens = ["", "/endpoints", "/endpoint", "/logs", "/transform", "/sandbox", "/alerts", "/secrets", "/api-keys", "/settings"];

for (const theme of ["dark", "light"] as const) {
  test(`screens in ${theme} theme`, async ({ page }) => {
    await page.addInitScript((t) => localStorage.setItem("theme", t), theme);
    await page.setViewportSize({ width: 1366, height: 820 });
    for (const locale of locales) {
      await page.goto(`/${locale}/login`);
      await expect(page.locator("form").filter({ visible: true })).toBeVisible();
      await page.screenshot({ path: `test-results/screens/${theme}-${locale}-login.png` });
    }
    await page.goto("/en/register");
    await page.getByLabel("Name", { exact: true }).filter({ visible: true }).fill("Ada");
    await page.getByLabel("Email", { exact: true }).filter({ visible: true }).fill(`shot-${theme}-${Date.now()}@example.com`);
    await page.getByLabel("Password", { exact: true }).filter({ visible: true }).fill("a long enough passphrase");
    await page.getByRole("button", { name: "Create account" }).click();
    await expect(page.getByRole("heading", { name: "Welcome, Ada" })).toBeVisible();
    await page.screenshot({ path: `test-results/screens/${theme}-en-onboarding.png` });
    const { id } = await createEndpoint(page, "Stripe");
    await page.getByRole("button", { name: "Send test" }).click();
    await expect(dialog(page).getByText("Delivery attempt 1 (200)")).toBeVisible({ timeout: 15_000 });

    for (const locale of locales) {
      for (const s of screens) {
        const path = s === "/endpoint" ? `/endpoints/${id}` : s;
        await page.goto(`/${locale}${path}`);
        await expect(page.locator("main h1").filter({ visible: true })).toBeVisible();
        await page.waitForTimeout(700); // charts, live feed, preview
        await page.screenshot({ path: `test-results/screens/${theme}-${locale}${s.replace("/", "-") || "-dashboard"}.png`, fullPage: true });
      }
    }
    await page.goto("/es/logs");
    await page.getByRole("row").nth(1).click();
    await expect(dialog(page).getByRole("tab").first()).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: `test-results/screens/${theme}-es-debugger.png` });
    await page.keyboard.press("Escape");
    await page.keyboard.press("Control+k");
    await page.waitForTimeout(200);
    await page.screenshot({ path: `test-results/screens/${theme}-es-palette.png` });
  });
}
