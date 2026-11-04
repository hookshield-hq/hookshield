"use client";

import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import { motion } from "motion/react";
import { cn } from "@/lib/cn";

export type SegmentOption<T extends string> = { value: T; label: ReactNode; description?: ReactNode; icon?: ReactNode };

/** Radio group drawn as a segmented control (range pickers, auth mode, retry strategy). */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  size = "md",
  className,
}: {
  options: SegmentOption<T>[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  size?: "sm" | "md";
  className?: string;
}) {
  const base = useId();
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const index = Math.max(0, options.findIndex((o) => o.value === value));

  function onKey(e: KeyboardEvent) {
    const delta = e.key === "ArrowRight" || e.key === "ArrowDown" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 0;
    if (!delta) return;
    e.preventDefault();
    const next = (index + delta + options.length) % options.length;
    onChange(options[next].value);
    refs.current[next]?.focus();
  }

  return (
    <div role="radiogroup" aria-label={label} onKeyDown={onKey} className={cn("inline-flex w-fit self-start rounded-lg border border-line bg-surface-strong/50 p-0.5", className)}>
      {options.map((o, i) => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(o.value)}
            className={cn(
              "relative inline-flex items-center gap-1.5 rounded-md font-medium transition",
              size === "sm" ? "h-7 px-2.5 text-xs" : "h-8 px-3 text-sm",
              active ? "text-fg" : "text-muted hover:text-fg",
            )}
          >
            {active && <motion.span layoutId={`${base}-pill`} transition={{ duration: 0.15 }} className="absolute inset-0 rounded-md bg-hover ring-1 ring-line" />}
            <span className="relative inline-flex items-center gap-1.5">
              {o.icon}
              {o.label}
            </span>
          </button>
        );
      })}
    </div>
  );
}

/** Radio group drawn as selectable cards with a description (auth mode, retry strategy, channel type). */
export function ChoiceCards<T extends string>({
  options,
  value,
  onChange,
  label,
  columns = 3,
}: {
  options: SegmentOption<T>[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  columns?: 2 | 3 | 4;
}) {
  const refs = useRef<Array<HTMLButtonElement | null>>([]);
  const index = Math.max(0, options.findIndex((o) => o.value === value));
  function onKey(e: KeyboardEvent) {
    const delta = e.key === "ArrowRight" || e.key === "ArrowDown" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 0;
    if (!delta) return;
    e.preventDefault();
    const next = (index + delta + options.length) % options.length;
    onChange(options[next].value);
    refs.current[next]?.focus();
  }
  const cols = { 2: "sm:grid-cols-2", 3: "sm:grid-cols-3", 4: "sm:grid-cols-2 lg:grid-cols-4" }[columns];
  return (
    <div role="radiogroup" aria-label={label} onKeyDown={onKey} className={cn("grid grid-cols-1 gap-2", cols)}>
      {options.map((o, i) => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(o.value)}
            className={cn(
              "flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition",
              active ? "border-accent bg-hover ring-2 ring-accent/20" : "border-line hover:border-subtle hover:bg-hover/50",
            )}
          >
            <span className="inline-flex items-center gap-2 text-sm font-medium text-fg">
              {o.icon}
              {o.label}
            </span>
            {o.description && <span className="text-xs text-muted">{o.description}</span>}
          </button>
        );
      })}
    </div>
  );
}
