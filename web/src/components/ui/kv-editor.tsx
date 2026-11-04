"use client";

import { Plus, Trash2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "./button";

export type KV = { key: string; value: string };

/** Editable key/value table (outbound headers, test event headers). */
export function KeyValueEditor({
  rows,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
  addLabel,
  max = 50,
}: {
  rows: KV[];
  onChange: (rows: KV[]) => void;
  keyPlaceholder: string;
  valuePlaceholder: string;
  addLabel: string;
  max?: number;
}) {
  const t = useTranslations("common");
  const set = (i: number, patch: Partial<KV>) => onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const input =
    "h-9 w-full rounded-md border border-line bg-surface-strong/60 px-2.5 font-mono text-xs text-fg outline-none transition placeholder:text-subtle focus:border-accent focus:ring-2 focus:ring-accent/25";
  return (
    <div className="flex flex-col gap-2">
      {rows.map((r, i) => (
        <div key={i} className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto] gap-2">
          <input aria-label={keyPlaceholder} placeholder={keyPlaceholder} value={r.key} onChange={(e) => set(i, { key: e.target.value })} className={input} />
          <input aria-label={valuePlaceholder} placeholder={valuePlaceholder} value={r.value} onChange={(e) => set(i, { value: e.target.value })} className={input} />
          <Button variant="ghost" size="icon" aria-label={t("remove")} onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <Trash2 className="size-4" />
          </Button>
        </div>
      ))}
      {rows.length < max && (
        <div>
          <Button variant="secondary" size="sm" onClick={() => onChange([...rows, { key: "", value: "" }])}>
            <Plus className="size-3.5" />
            {addLabel}
          </Button>
        </div>
      )}
    </div>
  );
}

export function toKV(obj: Record<string, string> | null | undefined): KV[] {
  return Object.entries(obj ?? {}).map(([key, value]) => ({ key, value }));
}

export function fromKV(rows: KV[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) if (r.key.trim()) out[r.key.trim()] = r.value;
  return out;
}
