// Public URLs of the project. Build-time overrides (NEXT_PUBLIC_*) let a
// preview deploy or a fork point the website somewhere else.

/** Canonical origin of this website (sitemap, canonical URLs, Open Graph). */
export const SITE_URL = (process.env.NEXT_PUBLIC_SITE_URL ?? "https://hookshield.dev").replace(/\/$/, "");

/** HookShield Cloud dashboard. Sign-up and sign-in links point here. */
export const APP_URL = (process.env.NEXT_PUBLIC_APP_URL ?? "https://app.hookshield.dev").replace(/\/$/, "");

/** The open-source repository. */
export const REPO = "hookshield-hq/hookshield";
export const GITHUB_URL = `https://github.com/${REPO}`;
export const GITHUB_BLOB = `${GITHUB_URL}/blob/main`;
export const GITHUB_TREE = `${GITHUB_URL}/tree/main`;

/** One-click deploys (docs/SELF_HOSTING.md). */
export const DEPLOY = {
  render: `https://render.com/deploy?repo=${GITHUB_URL}`,
  // Set once the template is published in Railway (deploy/railway/README.md);
  // until then the landing page shows no Railway button.
  railway: process.env.NEXT_PUBLIC_RAILWAY_TEMPLATE_URL ?? "",
  coolify: "/docs/self-hosting/#coolify",
  compose: "/docs/self-hosting/#docker-compose-on-your-server",
} as const;

export const CONTACT = {
  sales: "mailto:hello@hookshield.dev?subject=HookShield%20Enterprise",
  security: `${GITHUB_URL}/security/advisories/new`,
} as const;

export const CLI_INSTALL = `curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | sh`;
