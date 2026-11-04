#!/usr/bin/env node
// i18n gate of the website (guide D7), same rules as the dashboard's:
//
//  1. messages/en.json, pt-BR.json and es.json have exactly the same keys,
//     no empty values, and the same {placeholders} per key.
//  2. No JSX text or user-facing attribute (aria-label, placeholder, title,
//     alt, label) is a literal containing words — use t("…") instead.
//     Escape hatch for a deliberate literal: a {/* i18n-ignore */} comment or
//     // i18n-ignore on the line before.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import ts from "typescript";

const root = new URL("..", import.meta.url).pathname;
const locales = ["en", "pt-BR", "es"];
const problems = [];

// ── 1. key parity ─────────────────────────────────────────
function flatten(obj, prefix = "", out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object") flatten(v, key, out);
    else out[key] = v;
  }
  return out;
}
const placeholders = (s) => [...String(s).matchAll(/\{(\w+)/g)].map((m) => m[1]).sort().join(",");

const messages = Object.fromEntries(
  locales.map((l) => [l, flatten(JSON.parse(readFileSync(join(root, "messages", `${l}.json`), "utf8")))]),
);
const base = messages.en;
for (const l of locales) {
  for (const key of Object.keys(base)) {
    if (!(key in messages[l])) problems.push(`[${l}] missing key ${key}`);
    else if (String(messages[l][key]).trim() === "") problems.push(`[${l}] empty value for ${key}`);
    else if (placeholders(messages[l][key]) !== placeholders(base[key]))
      problems.push(`[${l}] placeholders of ${key} differ from en: {${placeholders(messages[l][key])}} vs {${placeholders(base[key])}}`);
  }
  for (const key of Object.keys(messages[l])) if (!(key in base)) problems.push(`[${l}] extra key ${key} (not in en)`);
}

function walk(dir, ext, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) {
      if (!["node_modules", ".next", "out"].includes(name)) walk(p, ext, out);
    } else if (ext.some((e) => p.endsWith(e))) out.push(p);
  }
  return out;
}

// ── 2. hardcoded strings in JSX ───────────────────────────
const userFacingAttrs = new Set(["aria-label", "aria-description", "placeholder", "title", "alt", "label"]);
const hasWords = (s) => /\p{L}{2,}/u.test(s);
let scanned = 0;
for (const file of walk(join(root, "src"), [".tsx"])) {
  scanned++;
  const text = readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const ignored = (node) => {
    const line = sf.getLineAndCharacterOfPosition(node.getStart()).line;
    const lines = text.split("\n");
    return /i18n-ignore/.test(lines[line] ?? "") || /i18n-ignore/.test(lines[line - 1] ?? "");
  };
  const report = (node, what) => {
    if (ignored(node)) return;
    const { line } = sf.getLineAndCharacterOfPosition(node.getStart());
    problems.push(`${relative(root, file)}:${line + 1}: hardcoded ${what} — use t("…")`);
  };
  const visit = (node) => {
    if (ts.isJsxText(node) && hasWords(node.text)) report(node, `text "${node.text.trim().slice(0, 40)}"`);
    if (ts.isJsxAttribute(node) && userFacingAttrs.has(node.name.getText(sf)) && node.initializer) {
      const init = node.initializer;
      const literal = ts.isStringLiteral(init)
        ? init.text
        : ts.isJsxExpression(init) && init.expression && ts.isStringLiteral(init.expression)
          ? init.expression.text
          : null;
      if (literal && hasWords(literal)) report(node, `${node.name.getText(sf)}="${literal.slice(0, 40)}"`);
    }
    ts.forEachChild(node, visit);
  };
  visit(sf);
}

if (problems.length) {
  console.error(`✗ i18n check failed (${problems.length} problem${problems.length > 1 ? "s" : ""}):`);
  for (const p of problems) console.error("  " + p);
  process.exit(1);
}
console.log(`✓ i18n OK: ${Object.keys(base).length} keys × ${locales.length} languages, ${scanned} components without hardcoded text`);
