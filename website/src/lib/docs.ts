import { existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkRehype from "remark-rehype";
import rehypeSlug from "rehype-slug";
import rehypeAutolinkHeadings from "rehype-autolink-headings";
import rehypeShiki from "@shikijs/rehype";
import rehypeStringify from "rehype-stringify";
import { visit } from "unist-util-visit";
import type { Root as MdRoot } from "mdast";
import type { Element, Root as HastRoot } from "hast";
import { GITHUB_BLOB, GITHUB_TREE } from "@/site";

// The docs pages are rendered at build time from the Markdown files of the
// repository (docs/*.md, SECURITY.md, CONTRIBUTING.md) — one source of truth,
// readable on GitHub and on the website. Links between documents become site
// links; links to other repository files go to GitHub.

/** Repository root (the website is built from website/). */
export const REPO_ROOT = path.resolve(process.cwd(), "..");

export type DocPage = {
  /** URL segment under /docs/ ("" is the docs home). */
  slug: string;
  /** Markdown file, relative to the repository root. */
  file: string;
  /** Sidebar label. */
  label: string;
};

export type DocSection = { id: "start" | "concepts" | "reference" | "project"; pages: DocPage[] };

export const docsNav: DocSection[] = [
  {
    id: "start",
    pages: [
      { slug: "", file: "docs/README.md", label: "Introduction" },
      { slug: "self-hosting", file: "docs/SELF_HOSTING.md", label: "Self-hosting" },
      { slug: "configuration", file: "docs/CONFIGURATION.md", label: "Configuration" },
      { slug: "cli", file: "docs/CLI.md", label: "CLI" },
    ],
  },
  {
    id: "concepts",
    pages: [
      { slug: "ingest", file: "docs/INGEST.md", label: "Receiving webhooks" },
      { slug: "transform", file: "docs/TRANSFORM.md", label: "Transform rules" },
      { slug: "delivery", file: "docs/DELIVERY.md", label: "Delivery and retries" },
      { slug: "alerts", file: "docs/ALERTS.md", label: "Alerts" },
      { slug: "architecture", file: "docs/ARCHITECTURE.md", label: "Architecture" },
    ],
  },
  {
    id: "reference",
    pages: [
      { slug: "api", file: "docs/API.md", label: "Control API" },
      { slug: "database", file: "docs/DATABASE.md", label: "Database" },
      { slug: "performance", file: "docs/PERFORMANCE.md", label: "Performance" },
    ],
  },
  {
    id: "project",
    pages: [
      { slug: "security", file: "SECURITY.md", label: "Security" },
      { slug: "development", file: "docs/DEVELOPMENT.md", label: "Development" },
      { slug: "contributing", file: "CONTRIBUTING.md", label: "Contributing" },
    ],
  },
];

export const allDocs: DocPage[] = docsNav.flatMap((s) => s.pages);
const byFile = new Map(allDocs.map((d) => [d.file, d]));

export function docHref(slug: string, hash = ""): string {
  return (slug ? `/docs/${slug}/` : "/docs/") + hash;
}

export function findDoc(slug: string): DocPage | undefined {
  return allDocs.find((d) => d.slug === slug);
}

/** A link found in a document, after rewriting. */
export type DocLink = {
  /** As written in the Markdown. */
  raw: string;
  /** What the page links to. */
  href: string;
  /** "doc" = another docs page (anchor checked by the tests), "repo" = a file on GitHub, "site" = a root-relative page of this site. */
  kind: "doc" | "repo" | "anchor" | "external" | "site";
  /** For doc and anchor links: target page slug and heading id. */
  target?: { slug: string; hash: string };
};

export type TocItem = { id: string; text: string; depth: 2 | 3 };

export type RenderedDoc = {
  page: DocPage;
  title: string;
  html: string;
  toc: TocItem[];
  links: DocLink[];
  /** Heading ids in the page, for anchor checks. */
  ids: string[];
  /** Plain-text summary (first paragraph) for the meta description. */
  description: string;
};

/** Rewrites one Markdown URL found in `file`. Exported for tests. */
export function rewriteUrl(url: string, file: string): DocLink {
  if (/^[a-z][a-z0-9+.-]*:/i.test(url) || url.startsWith("//")) return { raw: url, href: url, kind: "external" };
  // Root-relative links ("/legal/refund/", "/#pricing") already point at a page of this site.
  if (url.startsWith("/")) return { raw: url, href: url, kind: "site" };
  const self = byFile.get(file);
  if (url.startsWith("#")) {
    return { raw: url, href: url, kind: "anchor", target: { slug: self?.slug ?? "", hash: url.slice(1) } };
  }
  const [rawPath, rawHash = ""] = url.split("#");
  const hash = rawHash ? `#${rawHash}` : "";
  const resolved = path.posix.normalize(path.posix.join(path.posix.dirname(file), decodeURI(rawPath)));
  const doc = byFile.get(resolved);
  if (doc) return { raw: url, href: docHref(doc.slug, hash), kind: "doc", target: { slug: doc.slug, hash: rawHash } };
  if (resolved.startsWith("docs/assets/")) return { raw: url, href: `/docs-assets/${resolved.slice("docs/assets/".length)}`, kind: "repo" };
  const abs = path.join(REPO_ROOT, resolved.replace(/\/$/, ""));
  const isDir = existsSync(abs) && statSync(abs).isDirectory();
  return { raw: url, href: `${isDir ? GITHUB_TREE : GITHUB_BLOB}/${resolved.replace(/\/$/, "")}${hash}`, kind: "repo" };
}

function remarkRewriteLinks(options: { file: string; links: DocLink[] }) {
  return (tree: MdRoot) => {
    visit(tree, (node) => {
      if (node.type === "link" || node.type === "image" || node.type === "definition") {
        const link = rewriteUrl(node.url, options.file);
        if (node.type !== "image") options.links.push(link);
        node.url = link.href;
      }
    });
  };
}

function text(node: Element | HastRoot): string {
  let out = "";
  visit(node, "text", (t) => {
    out += t.value;
  });
  return out;
}

/** Pulls the H1 out of the tree (the page renders it) and collects the table of contents. */
function rehypeCollect(options: { out: { title: string; toc: TocItem[]; ids: string[]; description: string } }) {
  return (tree: HastRoot) => {
    const { out } = options;
    visit(tree, "element", (node, index, parent) => {
      if (node.tagName === "h1" && !out.title && parent && index !== undefined) {
        out.title = text(node).trim();
        parent.children.splice(index, 1);
        return index;
      }
      const id = typeof node.properties?.id === "string" ? node.properties.id : undefined;
      if (id) out.ids.push(id);
      if ((node.tagName === "h2" || node.tagName === "h3") && id) {
        out.toc.push({ id, text: text(node).trim(), depth: node.tagName === "h2" ? 2 : 3 });
      }
      if (node.tagName === "p" && !out.description && parent?.type === "root") {
        out.description = text(node).replace(/\s+/g, " ").trim();
      }
    });
    // External links open in a new tab, with no referrer leak.
    visit(tree, "element", (node) => {
      const href = node.tagName === "a" ? node.properties?.href : undefined;
      if (typeof href === "string" && /^https?:/.test(href)) {
        node.properties.target = "_blank";
        node.properties.rel = ["noopener", "noreferrer"];
      }
    });
  };
}

const cache = new Map<string, Promise<RenderedDoc>>();

/** Renders one docs page (cached for the build). */
export function renderDoc(page: DocPage): Promise<RenderedDoc> {
  let p = cache.get(page.file);
  if (!p) {
    p = render(page);
    cache.set(page.file, p);
  }
  return p;
}

async function render(page: DocPage): Promise<RenderedDoc> {
  const source = readFileSync(path.join(REPO_ROOT, page.file), "utf8");
  const rendered = await renderMarkdown(source, page.file);
  return { ...rendered, page, title: rendered.title || page.label };
}

export type RenderedMarkdown = Omit<RenderedDoc, "page">;

/**
 * Markdown → HTML with GitHub-flavoured Markdown, heading anchors, a table of
 * contents and highlighted code (light and dark). `file` (relative to the
 * repository root) is used to resolve relative links.
 */
export async function renderMarkdown(source: string, file: string): Promise<RenderedMarkdown> {
  const links: DocLink[] = [];
  const out = { title: "", toc: [] as TocItem[], ids: [] as string[], description: "" };
  const result = await unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkRewriteLinks, { file, links })
    .use(remarkRehype)
    .use(rehypeSlug)
    .use(rehypeCollect, { out }) // before the "#" anchors are appended to headings
    .use(rehypeAutolinkHeadings, {
      behavior: "append",
      properties: { className: ["anchor"], ariaHidden: "true", tabIndex: -1 },
      content: { type: "text", value: "#" },
    })
    .use(rehypeShiki, {
      themes: { light: "github-light-default", dark: "github-dark-default" },
      defaultColor: false,
      defaultLanguage: "text",
      fallbackLanguage: "text",
    })
    .use(rehypeStringify)
    .process(source);
  return {
    title: out.title,
    html: String(result),
    toc: out.toc,
    links,
    ids: out.ids,
    description: out.description.slice(0, 200),
  };
}

/** Previous and next pages in sidebar order. */
export function neighbours(slug: string): { prev?: DocPage; next?: DocPage } {
  const i = allDocs.findIndex((d) => d.slug === slug);
  return { prev: allDocs[i - 1], next: allDocs[i + 1] };
}

/** Repository URL to edit a page. */
export function editUrl(page: DocPage): string {
  return `${GITHUB_BLOB.replace("/blob/", "/edit/")}/${page.file}`;
}
