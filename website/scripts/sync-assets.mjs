#!/usr/bin/env node
// Copies the brand assets from their single source in the repository into
// public/ before dev and build (the copies are gitignored):
//   docs/assets/*                → public/docs-assets/  (images used by the docs)
//   docs/assets/*.webp           → public/brand/         (logos)
//   web/src/app/{icon,apple-icon,opengraph-image}.png, favicon.ico → public/brand/, public/favicon.ico
import { cpSync, mkdirSync, readdirSync } from "node:fs";
import { join } from "node:path";

const site = new URL("..", import.meta.url).pathname;
const repo = join(site, "..");
const out = (...p) => join(site, "public", ...p);

mkdirSync(out("docs-assets"), { recursive: true });
mkdirSync(out("brand"), { recursive: true });
for (const name of readdirSync(join(repo, "docs/assets"))) {
  cpSync(join(repo, "docs/assets", name), out("docs-assets", name));
  if (name.endsWith(".webp")) cpSync(join(repo, "docs/assets", name), out("brand", name));
}
for (const name of ["icon.png", "apple-icon.png", "opengraph-image.png"]) {
  cpSync(join(repo, "web/src/app", name), out("brand", name));
}
cpSync(join(repo, "web/src/app/favicon.ico"), out("favicon.ico"));
console.log("✓ brand assets copied to public/");
