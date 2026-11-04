import { readFileSync } from "node:fs";
import path from "node:path";
import { renderMarkdown, REPO_ROOT } from "./docs";

// Terms, privacy and refund policy of HookShield Cloud (website/content/legal/*.md).
// While `draft: true`, the page shows a banner and is not indexed.

export const legalSlugs = ["terms", "privacy", "refund"] as const;
export type LegalSlug = (typeof legalSlugs)[number];

export type LegalPage = { slug: LegalSlug; title: string; updated: string; draft: boolean; html: string; description: string };

export async function loadLegal(slug: LegalSlug): Promise<LegalPage> {
  const file = `website/content/legal/${slug}.md`;
  const raw = readFileSync(path.join(REPO_ROOT, file), "utf8");
  const match = raw.match(/^---\n([\s\S]*?)\n---\n/);
  if (!match) throw new Error(`${file}: missing front matter`);
  const meta = Object.fromEntries(
    match[1].split("\n").map((line) => {
      const i = line.indexOf(":");
      return [line.slice(0, i).trim(), line.slice(i + 1).trim()];
    }),
  );
  const rendered = await renderMarkdown(raw.slice(match[0].length), file);
  return {
    slug,
    title: meta.title ?? rendered.title,
    updated: meta.updated ?? "",
    draft: meta.draft === "true",
    html: rendered.html,
    description: rendered.description,
  };
}
