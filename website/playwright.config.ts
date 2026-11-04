import { defineConfig, devices } from "@playwright/test";

// The website tests run against the static export (npm run build first),
// served like a static host by scripts/serve.mjs.
export default defineConfig({
  testDir: "tests/e2e",
  timeout: 30_000,
  fullyParallel: true,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: { baseURL: process.env.SITE_URL ?? "http://localhost:3100", trace: "retain-on-failure" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "node scripts/serve.mjs out 3100",
    url: "http://localhost:3100/",
    reuseExistingServer: true,
    timeout: 30_000,
  },
});
