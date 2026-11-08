"use client";

import { useState, type KeyboardEvent, type ReactNode } from "react";

type Interval = "month" | "year";

/**
 * Monthly/yearly switch of the pricing section. The cards are rendered on the
 * server with both prices; this only sets data-interval on the wrapper, and
 * the cards show the matching price with group-data-[interval=…] classes.
 * Without JavaScript the monthly prices stay visible.
 */
export function BillingInterval({
  labels,
  children,
}: {
  labels: { group: string; month: string; year: string; save: string };
  children: ReactNode;
}) {
  const [interval, setInterval] = useState<Interval>("month");
  const options: Interval[] = ["month", "year"];
  function onKey(e: KeyboardEvent) {
    if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(e.key)) return;
    e.preventDefault();
    const next = interval === "month" ? "year" : "month";
    setInterval(next);
    document.getElementById(`billing-interval-${next}`)?.focus();
  }
  return (
    <div className="group" data-interval={interval}>
      <div className="mt-10 flex flex-wrap items-center justify-center gap-3">
        <div role="radiogroup" aria-label={labels.group} onKeyDown={onKey} className="inline-flex rounded-xl border border-line bg-surface-strong p-1">
          {options.map((o) => {
            const active = o === interval;
            return (
              <button
                key={o}
                id={`billing-interval-${o}`}
                type="button"
                role="radio"
                aria-checked={active}
                tabIndex={active ? 0 : -1}
                onClick={() => setInterval(o)}
                className={`rounded-lg px-4 py-1.5 text-sm font-medium transition ${active ? "bg-bg text-fg shadow-sm ring-1 ring-line" : "text-muted hover:text-fg"}`}
              >
                {labels[o]}
              </button>
            );
          })}
        </div>
        <span className="rounded-full bg-success/10 px-2.5 py-0.5 text-xs font-medium text-success">{labels.save}</span>
      </div>
      {children}
    </div>
  );
}
