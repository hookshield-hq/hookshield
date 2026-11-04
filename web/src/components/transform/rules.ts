// Editable drafts of transform rules (docs/TRANSFORM.md) and their conversion
// to and from the API document.

import type { BodyRule, HeaderRule, TransformRules } from "@/lib/queries";

export type BodyOp = BodyRule["op"];
export type HeaderOp = HeaderRule["op"];
export const BODY_OPS: BodyOp[] = ["rename", "remove", "set", "copy", "set_timestamp", "keep"];
export const HEADER_OPS: HeaderOp[] = ["set", "remove", "rename"];

export type BodyDraft = { id: number; op: BodyOp; path: string; from: string; to: string; valueText: string; format: "rfc3339" | "unix" | "unix_ms"; pathsText: string };
export type HeaderDraft = { id: number; op: HeaderOp; name: string; from: string; to: string; value: string };

let seq = 0;
const nextId = () => ++seq;

export function newBody(op: BodyOp): BodyDraft {
  return { id: nextId(), op, path: "", from: "", to: "", valueText: "", format: "rfc3339", pathsText: "" };
}

export function newHeader(op: HeaderOp): HeaderDraft {
  return { id: nextId(), op, name: "", from: "", to: "", value: "" };
}

/** A typed value: JSON when it parses ("42", "true", "{…}", "\"text\""), otherwise plain text. */
export function parseValue(text: string): unknown {
  const s = text.trim();
  if (s === "") return "";
  try {
    return JSON.parse(s);
  } catch {
    return text;
  }
}

export function valueText(v: unknown): string {
  return typeof v === "string" ? v : JSON.stringify(v);
}

export function toRules(body: BodyDraft[], headers: HeaderDraft[]): TransformRules | null {
  if (body.length === 0 && headers.length === 0) return null;
  const out: TransformRules = { version: 1 };
  if (body.length)
    out.body = body.map((r): BodyRule => {
      switch (r.op) {
        case "remove":
          return { op: "remove", path: r.path.trim() };
        case "rename":
        case "copy":
          return { op: r.op, from: r.from.trim(), to: r.to.trim() };
        case "set":
          return { op: "set", path: r.path.trim(), value: parseValue(r.valueText) };
        case "set_timestamp":
          return { op: "set_timestamp", path: r.path.trim(), format: r.format };
        case "keep":
          return {
            op: "keep",
            paths: r.pathsText
              .split(/[\s,]+/)
              .map((p) => p.trim())
              .filter(Boolean),
          };
      }
    });
  if (headers.length)
    out.headers = headers.map((r): HeaderRule => {
      if (r.op === "remove") return { op: "remove", name: r.name.trim() };
      if (r.op === "rename") return { op: "rename", from: r.from.trim(), to: r.to.trim() };
      return { op: "set", name: r.name.trim(), value: r.value };
    });
  return out;
}

export function fromRules(rules: TransformRules | null | undefined): { body: BodyDraft[]; headers: HeaderDraft[] } {
  return {
    body: (rules?.body ?? []).map((r) => ({
      ...newBody(r.op),
      path: r.path ?? "",
      from: r.from ?? "",
      to: r.to ?? "",
      valueText: r.op === "set" ? valueText(r.value) : "",
      format: r.format ?? "rfc3339",
      pathsText: (r.paths ?? []).join(", "),
    })),
    headers: (rules?.headers ?? []).map((r) => ({ ...newHeader(r.op), name: r.name ?? "", from: r.from ?? "", to: r.to ?? "", value: r.value ?? "" })),
  };
}
