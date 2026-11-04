import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

const landings = [
  { path: "/", lang: "en", title: "Webhooks that never get lost." },
  { path: "/pt-BR/", lang: "pt-BR", title: "Webhooks que nunca se perdem." },
  { path: "/es/", lang: "es", title: "Webhooks que nunca se pierden." },
];

function watchErrors(page: Page) {
  const errors: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  page.on("pageerror", (e) => errors.push(e.message));
  return errors;
}

for (const l of landings) {
  test(`landing ${l.lang}: language, sections, links to the dashboard`, async ({ page }) => {
    const errors = watchErrors(page);
    await page.goto(l.path);
    await expect(page.locator("html")).toHaveAttribute("lang", l.lang);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(l.title);
    for (const id of ["features", "self-host", "pricing", "faq"]) await expect(page.locator(`#${id}`)).toBeVisible();
    await expect(page.locator(`a[href="https://app.hookshield.dev/${l.lang}/register"]`).first()).toBeVisible();
    // hreflang alternates for every language
    for (const other of landings) await expect(page.locator(`link[rel="alternate"][hreflang="${other.lang}"]`)).toHaveCount(1);
    // every screenshot exists in this language and both themes
    for (const img of await page.locator('img[src^="/screens/"]').all()) {
      const src = await img.getAttribute("src");
      expect(src, "localized screenshot").toContain(`-${l.lang}.webp`);
      const res = await page.request.get(src!);
      expect(res.status(), src!).toBe(200);
    }
    expect(errors).toEqual([]);
  });
}

test("language menu and theme toggle", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Language: English").click();
  await page.getByRole("link", { name: "Português" }).click();
  await expect(page).toHaveURL(/\/pt-BR\/$/);
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.getByRole("button", { name: "Alternar tema" }).click();
  await expect(page.locator("html")).not.toHaveClass(/dark/);
  await page.reload();
  await expect(page.locator("html")).not.toHaveClass(/dark/); // remembered
});

test("mobile menu", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto("/es/");
  const menu = page.locator("header details").filter({ has: page.locator("summary[aria-label='Menú']") });
  await menu.locator("summary").click();
  await menu.getByRole("link", { name: "Precios" }).click();
  await expect(page.locator("#pricing")).toBeInViewport();
});

test("docs: sidebar, table of contents, previous and next", async ({ page }) => {
  await page.goto("/docs/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("HookShield documentation");
  await page.getByRole("navigation", { name: "Docs navigation" }).getByRole("link", { name: "CLI" }).click();
  await expect(page).toHaveURL(/\/docs\/cli\/$/);
  await expect(page.getByRole("link", { name: "CLI", exact: true }).first()).toHaveAttribute("aria-current", "page");
  await page.getByRole("navigation", { name: "On this page" }).getByRole("link", { name: "Install" }).click();
  await expect(page).toHaveURL(/#install$/);
  await page.getByRole("link", { name: /Next/ }).click();
  await expect(page).toHaveURL(/\/docs\/ingest\/$/);
});

test("unknown pages answer the 404 page", async ({ page }) => {
  const res = await page.goto("/docs/nope/");
  expect(res?.status()).toBe(404);
  await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
});

test("every internal link of the site resolves", async ({ page, request }) => {
  test.setTimeout(120_000);
  const seen = new Set<string>();
  const queue = ["/", "/pt-BR/", "/es/", "/docs/", "/legal/terms/", "/legal/privacy/", "/legal/refund/"];
  const broken: string[] = [];
  while (queue.length) {
    const path = queue.shift()!;
    if (seen.has(path)) continue;
    seen.add(path);
    const res = await request.get(path);
    if (res.status() !== 200) {
      broken.push(`${path} → ${res.status()}`);
      continue;
    }
    if (!(res.headers()["content-type"] ?? "").includes("text/html")) continue;
    await page.goto(path);
    const ids = new Set(await page.locator("[id]").evaluateAll((els) => els.map((e) => e.id)));
    for (const href of await page.locator("a[href]").evaluateAll((els) => els.map((a) => a.getAttribute("href")!))) {
      if (/^(https?:|mailto:)/.test(href)) continue;
      const [p, hash] = href.split("#");
      if (!p) {
        if (hash && !ids.has(hash)) broken.push(`${path}: #${hash} has no target`);
        continue;
      }
      if (!seen.has(p)) queue.push(p);
    }
  }
  expect(broken).toEqual([]);
  expect(seen.size).toBeGreaterThanOrEqual(20); // 3 landing pages, every docs page, the legal pages
});

const a11yPages = ["/", "/pt-BR/", "/es/", "/docs/", "/docs/api/", "/docs/cli/", "/legal/terms/", "/legal/privacy/", "/legal/refund/", "/docs/nope/"];
for (const theme of ["dark", "light"] as const) {
  test(`accessibility (WCAG 2.1 AA), ${theme} theme`, async ({ page }) => {
    test.setTimeout(120_000);
    await page.addInitScript((t) => localStorage.setItem("theme", t), theme);
    const violations: string[] = [];
    for (const path of a11yPages) {
      await page.goto(path);
      // open every disclosure so its content is checked too
      await page.locator("details:not([open])").evaluateAll((els) => els.forEach((e) => e.setAttribute("open", "")));
      const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
      for (const v of result.violations) {
        violations.push(`${path} ${v.id}: ${v.nodes.map((n) => n.target.join(" ")).slice(0, 3).join(", ")}`);
      }
    }
    expect(violations).toEqual([]);
  });
}
