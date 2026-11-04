import { expect, type Page } from "@playwright/test";

export const password = "a long enough passphrase";
export const uniqueEmail = (tag: string) => `e2e-${tag}-${Date.now()}-${Math.floor(Math.random() * 1e6)}@example.com`;

/** Signs up a fresh account in English and waits for the dashboard. */
export async function register(page: Page, email = uniqueEmail("user"), name = "Ada") {
  await page.goto("/en/register");
  await page.getByLabel("Name", { exact: true }).filter({ visible: true }).fill(name);
  await page.getByLabel("Email", { exact: true }).filter({ visible: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).filter({ visible: true }).fill(password);
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByRole("heading", { name: `Welcome, ${name}` })).toBeVisible();
  return email;
}

/**
 * Confirms the account's email with the link from the verification email,
 * read from Mailpit (HS_MAILPIT_URL, default http://localhost:8025 — the dev
 * stack catches every email there).
 */
export async function verifyEmail(page: Page, email: string) {
  const mailpit = process.env.HS_MAILPIT_URL ?? "http://localhost:8025";
  let link = "";
  await expect(async () => {
    const res = await page.request.get(`${mailpit}/api/v1/search?query=${encodeURIComponent(`to:"${email}"`)}`);
    const { messages } = (await res.json()) as { messages: { ID: string }[] };
    expect(messages.length).toBeGreaterThan(0);
    const msg = (await (await page.request.get(`${mailpit}/api/v1/message/${messages[0].ID}`)).json()) as { Text: string };
    link = /https?:\/\/\S+\/verify-email\?token=[\w-]+/.exec(msg.Text)?.[0] ?? "";
    expect(link).not.toBe("");
  }).toPass({ timeout: 15_000 });
  await page.goto(new URL(link).pathname + new URL(link).search);
  await expect(page.getByText("Email confirmed. You're all set.")).toBeVisible();
}

/**
 * Collects browser errors (missing translations, React errors, failed API
 * calls). The 401 of GET /v1/me before signing in is expected.
 */
export function watchErrors(page: Page, expected: number[] = []): () => string[] {
  const errors: string[] = [];
  const allowed = new RegExp(`status of (${[401, ...expected].join("|")})\\b`);
  page.on("console", (m) => {
    if (m.type() === "error" && !allowed.test(m.text())) errors.push(m.text().split("\n")[0]);
  });
  page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
  return () => errors;
}

/** The visible dialog (Next keeps hidden copies of visited pages mounted). */
export const dialog = (page: Page) => page.getByRole("dialog").filter({ visible: true }).last();

/** Creates an endpoint from the Endpoints screen and closes the one-time token window. */
export async function createEndpoint(page: Page, name: string, target = "http://localhost:4000/e2e") {
  await page.goto("/en/endpoints");
  await page.getByRole("button", { name: "New endpoint" }).filter({ visible: true }).first().click();
  const d = dialog(page);
  await d.getByLabel("Name", { exact: true }).fill(name);
  await d.getByLabel("Destination URL").fill(target);
  await page.getByRole("button", { name: "Create endpoint" }).click();
  const reveal = dialog(page);
  await expect(reveal.getByRole("heading", { name: "Endpoint created" })).toBeVisible();
  const token = await reveal.locator("code").nth(1).textContent();
  expect(token).toMatch(/^hs_in_/);
  await reveal.getByLabel("I have copied it to a safe place").check();
  await reveal.getByRole("button", { name: "Done" }).click();
  await expect(page).toHaveURL(/\/en\/endpoints\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { name })).toBeVisible();
  return { token: token!, id: page.url().split("/").pop()! };
}
