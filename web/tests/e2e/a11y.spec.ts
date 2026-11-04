import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";
import { createEndpoint, dialog, register, uniqueEmail } from "./helpers";

// Accessibility (WCAG 2.1 A + AA, axe-core) of every screen in both themes:
// contrast, names of controls, landmarks, ARIA, dialogs. Runs against the real
// stack like the other e2e tests.

const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];

async function audit(page: Page, label: string, found: string[]) {
  // Let entrance animations finish: axe would measure the contrast of half-faded text.
  await page.waitForTimeout(400);
  const { violations } = await new AxeBuilder({ page }).withTags(tags).analyze();
  for (const v of violations) {
    const targets = v.nodes
      .slice(0, 3)
      .map((n) => {
        const d = n.any[0]?.data as { fgColor?: string; bgColor?: string; contrastRatio?: number } | undefined;
        return n.target.join(" ") + (d?.contrastRatio ? ` [${d.fgColor} on ${d.bgColor} = ${d.contrastRatio}]` : "");
      })
      .join(" | ");
    found.push(`${label}: ${v.id} (${v.impact}) ×${v.nodes.length} — ${targets}`);
  }
}

async function apiURL(page: Page) {
  const res = await page.request.get("/api/config");
  return ((await res.json()) as { apiUrl: string }).apiUrl;
}

for (const theme of ["dark", "light"] as const) {
  test(`WCAG 2.1 AA in the ${theme} theme`, async ({ page, baseURL }) => {
    test.setTimeout(120_000);
    await page.addInitScript((t) => localStorage.setItem("theme", t), theme);
    const found: string[] = [];

    for (const path of ["/en/login", "/en/register", "/en/forgot-password"]) {
      await page.goto(path);
      await expect(page.locator("main")).toBeVisible();
      await audit(page, `${theme} ${path}`, found);
    }

    await register(page, uniqueEmail(`a11y-${theme}`));
    // The account preference would override the theme chosen above.
    const api = await apiURL(page);
    await page.request.patch(`${api}/v1/me`, { data: { theme_preference: theme }, headers: { Origin: baseURL! } });
    await audit(page, `${theme} /en (onboarding)`, found);

    const { id } = await createEndpoint(page, `A11y ${theme}`);
    await page.request.post(`${api}/v1/endpoints/${id}/test`, { data: {}, headers: { Origin: baseURL! } });

    for (const path of ["", "/endpoints", `/endpoints/${id}`, "/logs", "/transform", "/sandbox", "/alerts", "/secrets", "/api-keys", "/settings"]) {
      await page.goto(`/en${path}`);
      await expect(page.locator("main h1").first()).toBeVisible();
      await audit(page, `${theme} /en${path}`, found);
    }

    // Request debugger.
    await page.goto("/en/logs");
    await page.getByRole("row").filter({ hasText: "hookshield.test" }).first().click();
    await expect(dialog(page).getByText("Delivered", { exact: true }).first()).toBeVisible({ timeout: 15_000 });
    await audit(page, `${theme} debugger`, found);
    await page.keyboard.press("Escape");

    // A dialog and the command palette.
    await page.goto("/en/endpoints");
    await page.getByRole("button", { name: "New endpoint" }).filter({ visible: true }).first().click();
    await expect(dialog(page)).toBeVisible();
    await audit(page, `${theme} new-endpoint dialog`, found);
    await page.keyboard.press("Escape");
    await page.keyboard.press("ControlOrMeta+k");
    await expect(dialog(page)).toBeVisible();
    await audit(page, `${theme} command palette`, found);

    expect(found, found.join("\n")).toEqual([]);
  });
}
