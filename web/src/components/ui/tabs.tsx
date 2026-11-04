"use client";

import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import { motion } from "motion/react";
import { cn } from "@/lib/cn";

export type Tab = { id: string; label: ReactNode; content: ReactNode };

/** Tabs with arrow-key navigation and an animated underline. */
export function Tabs({
  tabs,
  value,
  onChange,
  className,
  label,
}: {
  tabs: Tab[];
  value: string;
  onChange: (id: string) => void;
  className?: string;
  label: string;
}) {
  const base = useId();
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const index = Math.max(0, tabs.findIndex((t) => t.id === value));

  function onKey(e: KeyboardEvent) {
    const delta = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (!delta) return;
    e.preventDefault();
    const next = (index + delta + tabs.length) % tabs.length;
    onChange(tabs[next].id);
    refs.current[next]?.focus();
  }

  return (
    <div className={className}>
      <div role="tablist" aria-label={label} className="flex gap-1 border-b border-line" onKeyDown={onKey}>
        {tabs.map((t, i) => {
          const active = t.id === value;
          return (
            <button
              key={t.id}
              ref={(el) => {
                refs.current[i] = el;
              }}
              role="tab"
              id={`${base}-tab-${t.id}`}
              aria-selected={active}
              aria-controls={`${base}-panel-${t.id}`}
              tabIndex={active ? 0 : -1}
              onClick={() => onChange(t.id)}
              className={cn("relative px-3 py-2 text-sm transition", active ? "text-fg" : "text-muted hover:text-fg")}
            >
              {t.label}
              {active && (
                <motion.span layoutId={`${base}-underline`} className="bg-accent-gradient absolute inset-x-2 -bottom-px h-0.5 rounded-full" />
              )}
            </button>
          );
        })}
      </div>
      {tabs.map((t) => (
        <div
          key={t.id}
          role="tabpanel"
          id={`${base}-panel-${t.id}`}
          aria-labelledby={`${base}-tab-${t.id}`}
          hidden={t.id !== value}
          className="pt-4"
        >
          {t.id === value && t.content}
        </div>
      ))}
    </div>
  );
}
