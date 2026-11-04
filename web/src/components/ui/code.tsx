"use client";

import { Fragment, useMemo, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { CopyButton } from "./copy-button";

const TOKEN = /("(?:\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(?:true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+-]?\d+)?)/g;

/** Pretty JSON with syntax colours (keys, strings, numbers, literals). */
export function highlightJSON(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let i = 0;
  for (const m of text.matchAll(TOKEN)) {
    const start = m.index ?? 0;
    if (start > last) out.push(<Fragment key={i++}>{text.slice(last, start)}</Fragment>);
    const tok = m[0];
    let cls = "text-info";
    if (tok.startsWith('"')) cls = m[2] ? "text-accent" : "text-success";
    else if (tok === "true" || tok === "false" || tok === "null") cls = "text-warning";
    out.push(
      <span key={i++} className={cls}>
        {tok}
      </span>,
    );
    last = start + tok.length;
  }
  if (last < text.length) out.push(<Fragment key={i++}>{text.slice(last)}</Fragment>);
  return out;
}

export function prettyJSON(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2) ?? "";
  } catch {
    return String(value);
  }
}

/** Read-only code panel with a copy button. JSON values are pretty-printed and coloured. */
export function CodeBlock({
  value,
  json,
  className,
  copy = true,
  maxHeight = "max-h-96",
}: {
  value: unknown;
  json?: boolean;
  className?: string;
  copy?: boolean;
  maxHeight?: string;
}) {
  const text = useMemo(() => (json ? prettyJSON(value) : String(value ?? "")), [value, json]);
  const content = useMemo(() => (json ? highlightJSON(text) : text), [json, text]);
  return (
    <div className={cn("group relative rounded-lg border border-line bg-surface-strong/70", className)}>
      {/* tabIndex: a scrollable region must be reachable with the keyboard (WCAG 2.1.1). */}
      <pre
        tabIndex={0}
        className={cn("overflow-auto rounded-lg p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-fg focus-visible:ring-2 focus-visible:ring-accent/60 focus-visible:outline-none", maxHeight)}
      >
        {content}
      </pre>
      {copy && text && <CopyButton value={text} className="absolute top-1.5 right-1.5 bg-surface-strong/80 opacity-0 transition group-hover:opacity-100 focus:opacity-100" />}
    </div>
  );
}

/** Headers as a compact two-column list. */
export function HeaderList({ headers, empty }: { headers: Record<string, string | string[]> | null | undefined; empty: ReactNode }) {
  const entries = Object.entries(headers ?? {}).sort(([a], [b]) => a.localeCompare(b));
  if (entries.length === 0) return <p className="text-xs text-subtle">{empty}</p>;
  return (
    <dl className="grid grid-cols-[minmax(8rem,auto)_1fr] gap-x-4 gap-y-1.5 rounded-lg border border-line bg-surface-strong/70 p-3 font-mono text-xs">
      {entries.map(([k, v]) => (
        <Fragment key={k}>
          <dt className="text-accent">{k}</dt>
          <dd className="break-all text-fg">{Array.isArray(v) ? v.join(", ") : v}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

/** Parses JSON typed by the user; returns undefined for empty text. */
export function parseJSON(text: string): { ok: true; value: unknown } | { ok: false } {
  if (!text.trim()) return { ok: true, value: undefined };
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch {
    return { ok: false };
  }
}
