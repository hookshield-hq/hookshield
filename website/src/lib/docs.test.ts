import { existsSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { allDocs, findDoc, REPO_ROOT, renderDoc, rewriteUrl } from "./docs";
import { GITHUB_BLOB, GITHUB_TREE } from "@/site";

describe("rewriteUrl", () => {
  it("turns links between docs into site links", () => {
    expect(rewriteUrl("CONFIGURATION.md", "docs/API.md").href).toBe("/docs/configuration/");
    expect(rewriteUrl("DELIVERY.md#retries", "docs/API.md").href).toBe("/docs/delivery/#retries");
    expect(rewriteUrl("../SECURITY.md", "docs/README.md").href).toBe("/docs/security/");
    expect(rewriteUrl("docs/ARCHITECTURE.md#security-model", "SECURITY.md").href).toBe("/docs/architecture/#security-model");
    expect(rewriteUrl("README.md", "docs/CLI.md").href).toBe("/docs/");
  });

  it("sends other repository paths to GitHub", () => {
    expect(rewriteUrl("../deploy/docker-compose.yml", "docs/SELF_HOSTING.md").href).toBe(`${GITHUB_BLOB}/deploy/docker-compose.yml`);
    expect(rewriteUrl("../deploy/railway/", "docs/SELF_HOSTING.md").href).toBe(`${GITHUB_TREE}/deploy/railway`);
    expect(rewriteUrl(".", "docs/README.md").href).toBe(`${GITHUB_TREE}/docs`);
  });

  it("serves the brand assets from the site", () => {
    expect(rewriteUrl("assets/only-icon.webp", "docs/README.md").href).toBe("/docs-assets/only-icon.webp");
  });

  it("leaves external links and anchors alone", () => {
    expect(rewriteUrl("https://resend.com", "docs/CONFIGURATION.md")).toMatchObject({ kind: "external", href: "https://resend.com" });
    expect(rewriteUrl("mailto:a@b.c", "docs/README.md").kind).toBe("external");
    expect(rewriteUrl("#images", "docs/SELF_HOSTING.md")).toMatchObject({ kind: "anchor", target: { slug: "self-hosting", hash: "images" } });
  });
});

describe("rewriteUrl with root-relative links", () => {
  it("keeps them as site links, also in the legal pages", () => {
    for (const url of ["/legal/refund/", "/#pricing", "/legal/privacy/", "/docs/security/"]) {
      expect(rewriteUrl(url, "website/content/legal/terms.md")).toMatchObject({ kind: "site", href: url });
    }
    expect(rewriteUrl("/docs/", "docs/README.md")).toMatchObject({ kind: "site", href: "/docs/" });
  });

  it("still treats protocol-relative links as external", () => {
    expect(rewriteUrl("//example.com/x", "website/content/legal/terms.md").kind).toBe("external");
  });
});

describe("every docs page", () => {
  it("exists and has a unique slug", () => {
    for (const page of allDocs) expect(existsSync(path.join(REPO_ROOT, page.file)), page.file).toBe(true);
    expect(new Set(allDocs.map((d) => d.slug)).size).toBe(allDocs.length);
  });

  it("renders with a title, and every link resolves", async () => {
    const rendered = await Promise.all(allDocs.map(renderDoc));
    const ids = new Map(rendered.map((r) => [r.page.slug, new Set(r.ids)]));
    const broken: string[] = [];
    for (const doc of rendered) {
      expect(doc.title, doc.page.file).not.toBe("");
      expect(doc.html, doc.page.file).not.toContain("<h1");
      for (const link of doc.links) {
        if (link.kind === "doc" || link.kind === "anchor") {
          const { slug, hash } = link.target!;
          if (!findDoc(slug)) broken.push(`${doc.page.file}: ${link.raw} → no page "${slug}"`);
          else if (hash && !ids.get(slug)!.has(hash)) broken.push(`${doc.page.file}: ${link.raw} → no heading #${hash}`);
        }
        if (link.kind === "repo" && link.href.startsWith("https://github.com/")) {
          const repoPath = link.href.replace(/^https:\/\/github\.com\/[^/]+\/[^/]+\/(blob|tree)\/main\//, "").split("#")[0];
          if (!existsSync(path.join(REPO_ROOT, repoPath))) broken.push(`${doc.page.file}: ${link.raw} → ${repoPath} does not exist`);
        }
        if (link.kind === "repo" && link.href.startsWith("/docs-assets/")) {
          const file = path.join(REPO_ROOT, "docs/assets", link.href.slice("/docs-assets/".length));
          if (!existsSync(file)) broken.push(`${doc.page.file}: ${link.raw} → missing asset`);
        }
      }
    }
    expect(broken).toEqual([]);
  });

  it("highlights code for both themes", async () => {
    const doc = await renderDoc(findDoc("cli")!);
    expect(doc.html).toContain("--shiki-dark");
    expect(doc.html).toContain("--shiki-light");
    expect(doc.toc.length).toBeGreaterThan(2);
  });
});
