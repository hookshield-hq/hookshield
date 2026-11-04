import { expect, test } from "@playwright/test";
import { password, register, uniqueEmail } from "./helpers";

test("the root redirects to the browser's language and the dashboard asks to sign in", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/en\/login\?next=%2F$/);
  await expect(page.getByRole("heading", { name: "Sign in to HookShield" })).toBeVisible();
});

test("Portuguese and Spanish browsers land on their language", async ({ browser }) => {
  for (const [browserLocale, path, heading] of [
    ["pt-BR", "/pt-BR/login", "Entrar no HookShield"],
    ["es-ES", "/es/login", "Inicia sesión en HookShield"],
  ]) {
    const ctx = await browser.newContext({ locale: browserLocale }); // sets Accept-Language
    const page = await ctx.newPage();
    await page.goto("/");
    await expect(page).toHaveURL(new RegExp(path.replace("/", "\\/")));
    await expect(page.getByRole("heading", { name: heading })).toBeVisible();
    await ctx.close();
  }
});

test("sign up, use the shell, sign out and sign in again", async ({ page }) => {
  const email = uniqueEmail("flow");
  await register(page, email);

  // Shell: sidebar, cluster status from the API, user menu.
  await expect(page.getByRole("navigation", { name: "Main navigation" }).getByRole("link", { name: "Endpoints" })).toBeVisible();
  await expect(page.getByRole("status", { name: "Cluster status" })).toContainText("Operational");

  // ⌘K palette navigates.
  await page.keyboard.press("Control+k");
  await page.getByPlaceholder("Type a command").fill("API keys");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/en\/api-keys$/);
  await expect(page.getByRole("heading", { name: "API keys", exact: true })).toBeVisible();

  // Sign out → login page.
  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/en\/login/);

  // Wrong password: translated API error. Right password: back in.
  await page.getByLabel("Email", { exact: true }).filter({ visible: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).filter({ visible: true }).fill("wrong password!!");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator("[role=alert]:not(#__next-route-announcer__)")).toHaveText("Wrong email or password.");
  await page.getByLabel("Password", { exact: true }).filter({ visible: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  // Back to the page the user was on.
  await expect(page).toHaveURL(/\/en\/api-keys$/);
  await expect(page.getByRole("heading", { name: "API keys", exact: true })).toBeVisible();
});

test("changing the language updates the URL, the texts and the account", async ({ page }) => {
  await register(page, uniqueEmail("lang"), "Bia");
  await page.getByRole("button", { name: "Language" }).click();
  await page.getByRole("menuitemradio", { name: "Português (Brasil)" }).click();
  await expect(page).toHaveURL(/\/pt-BR$/);
  await expect(page.getByRole("heading", { name: "Olá, Bia" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Navegação principal" })).toBeVisible();

  // Saved on the account: after signing out and in from English, the dashboard comes back in Portuguese.
  const saved = await page.evaluate(async () => {
    const { apiUrl } = await (await fetch("/api/config")).json();
    return (await (await fetch(`${apiUrl}/v1/me`, { credentials: "include" })).json()).user.locale;
  });
  expect(saved).toBe("pt-BR");
});

test("client-side validation and API errors are translated", async ({ page }) => {
  await page.goto("/es/register");
  await page.getByRole("button", { name: "Crear cuenta" }).click();
  await expect(page.getByText("Introduce un correo válido.")).toBeVisible();
  await expect(page.getByText("Usa al menos 10 caracteres.")).toBeVisible();

  const email = uniqueEmail("dup");
  await page.getByLabel("Correo electrónico", { exact: true }).filter({ visible: true }).fill(email);
  await page.getByLabel("Contraseña", { exact: true }).filter({ visible: true }).fill(password);
  await page.getByRole("button", { name: "Crear cuenta" }).click();
  await expect(page.getByRole("heading", { name: "Hola" })).toBeVisible();

  const ctx = await page.context().browser()!.newContext();
  const other = await ctx.newPage();
  await other.goto("/es/register");
  await other.getByLabel("Correo electrónico", { exact: true }).filter({ visible: true }).fill(email);
  await other.getByLabel("Contraseña", { exact: true }).filter({ visible: true }).fill(password);
  await other.getByRole("button", { name: "Crear cuenta" }).click();
  await expect(other.locator("[role=alert]:not(#__next-route-announcer__)")).toHaveText("Ya existe una cuenta con este correo.");
  await ctx.close();
});

test("unknown pages show a translated 404", async ({ page }) => {
  await page.goto("/pt-BR/nao-existe");
  await expect(page.getByRole("heading", { name: "Página não encontrada" })).toBeVisible();
});

test("a typed password does not stay in hidden pages after navigating away", async ({ page }) => {
  await page.goto("/en/login");
  await page.getByLabel("Password", { exact: true }).filter({ visible: true }).fill("secret typed here");
  await page.getByRole("link", { name: "Create an account" }).click();
  await expect(page.getByRole("heading", { name: "Create your account" })).toBeVisible();
  // Next.js keeps the login page in the DOM (hidden): its password field must be empty.
  const values = await page.locator('input[type="password"]').evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value));
  expect(values.some((v) => v.includes("secret typed here"))).toBe(false);
});
