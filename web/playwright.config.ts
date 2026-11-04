import { defineConfig, devices } from "@playwright/test";

// End-to-end tests run against a real stack: the Go services (make dev or
// make up) and this app on :3000 (started here unless already running).
export default defineConfig({
  testDir: "tests/e2e",
  timeout: 30_000,
  fullyParallel: false,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: process.env.WEB_URL ?? "http://localhost:3000",
    trace: "retain-on-failure",
    locale: "en-US",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "npm run start -- -p 3000",
    url: "http://localhost:3000/api/config",
    reuseExistingServer: true,
    timeout: 60_000,
  },
});
