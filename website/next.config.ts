import type { NextConfig } from "next";

// The public website (landing page + docs) is a fully static export: `npm run
// build` writes plain HTML to out/, which any static host can serve (GitHub
// Pages, Cloudflare Pages, a bucket, Caddy). No server, no cookies, no tracking.
const nextConfig: NextConfig = {
  output: "export",
  trailingSlash: true, // /docs/cli/ → docs/cli/index.html on every static host
  images: { unoptimized: true },
  poweredByHeader: false,
  // Two root layouts ((en) and [locale]) → one global 404 page (out/404.html).
  experimental: { globalNotFound: true },
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
};

export default nextConfig;
