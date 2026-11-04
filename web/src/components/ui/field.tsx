"use client";

import { forwardRef, useId, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/cn";

const control =
  "w-full rounded-lg border border-line bg-surface-strong/60 text-sm text-fg outline-none transition placeholder:text-subtle focus:border-accent focus:ring-2 focus:ring-accent/25";

function Describe({ id, error, hint }: { id: string; error?: ReactNode; hint?: ReactNode }) {
  if (error)
    return (
      <p id={`${id}-error`} role="alert" className="text-xs text-danger">
        {error}
      </p>
    );
  if (hint)
    return (
      <p id={`${id}-hint`} className="text-xs text-subtle">
        {hint}
      </p>
    );
  return null;
}

export type SelectProps = SelectHTMLAttributes<HTMLSelectElement> & { label?: ReactNode; hint?: ReactNode; error?: ReactNode };

/** Native select styled like the inputs (keyboard and screen readers for free). */
export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select({ label, hint, error, className, id, children, ...rest }, ref) {
  const auto = useId();
  const sid = id ?? auto;
  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label htmlFor={sid} className="text-xs font-medium text-muted">
          {label}
        </label>
      )}
      <div className="relative">
        <select
          ref={ref}
          id={sid}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${sid}-error` : hint ? `${sid}-hint` : undefined}
          className={cn(control, "h-10 appearance-none pr-9 pl-3", error && "border-danger", className)}
          {...rest}
        >
          {children}
        </select>
        <ChevronDown className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-subtle" />
      </div>
      <Describe id={sid} error={error} hint={hint} />
    </div>
  );
});

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & {
  label?: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  mono?: boolean;
};

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { label, hint, error, mono, className, id, ...rest },
  ref,
) {
  const auto = useId();
  const tid = id ?? auto;
  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label htmlFor={tid} className="text-xs font-medium text-muted">
          {label}
        </label>
      )}
      <textarea
        ref={ref}
        id={tid}
        spellCheck={mono ? false : undefined}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${tid}-error` : hint ? `${tid}-hint` : undefined}
        className={cn(control, "min-h-24 px-3 py-2", mono && "font-mono text-xs leading-relaxed", error && "border-danger", className)}
        {...rest}
      />
      <Describe id={tid} error={error} hint={hint} />
    </div>
  );
});

/** A titled group of fields inside a form. */
export function Fieldset({ legend, description, children, className }: { legend: ReactNode; description?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <fieldset className={cn("flex flex-col gap-4", className)}>
      <div>
        <legend className="text-sm font-semibold text-fg">{legend}</legend>
        {description && <p className="mt-0.5 text-xs text-muted">{description}</p>}
      </div>
      {children}
    </fieldset>
  );
}
