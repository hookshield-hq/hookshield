"use client";

import { Check, Copy } from "lucide-react";
import { useState } from "react";

/** Copies a command; the icon turns into a check for a moment. */
export function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      aria-label={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        } catch {
          // Clipboard blocked (insecure context, permissions): the text is still selectable.
        }
      }}
      className="inline-flex size-8 shrink-0 items-center justify-center rounded-lg text-muted transition hover:bg-hover hover:text-fg"
    >
      {copied ? <Check className="size-4 text-success" aria-hidden /> : <Copy className="size-4" aria-hidden />}
    </button>
  );
}
