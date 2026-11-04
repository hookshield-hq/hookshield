"use client";

import { useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { Check, Copy } from "lucide-react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/cn";

/** Copies text with instant visual feedback (icon morphs into a check). */
export function CopyButton({ value, className, label }: { value: string; className?: string; label?: string }) {
  const t = useTranslations("common");
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      return;
    }
    setCopied(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  }

  return (
    <button
      type="button"
      onClick={copy}
      aria-label={copied ? t("copied") : (label ?? t("copy"))}
      title={copied ? t("copied") : (label ?? t("copy"))}
      className={cn(
        "relative inline-flex size-8 items-center justify-center rounded-md text-muted transition hover:bg-hover hover:text-fg",
        copied && "text-success hover:text-success",
        className,
      )}
    >
      <AnimatePresence initial={false} mode="popLayout">
        <motion.span
          key={copied ? "check" : "copy"}
          initial={{ scale: 0.4, opacity: 0 }}
          animate={{ scale: 1, opacity: 1 }}
          exit={{ scale: 0.4, opacity: 0 }}
          transition={{ duration: 0.12, ease: "easeOut" }}
          className="flex"
        >
          {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
        </motion.span>
      </AnimatePresence>
      <span aria-live="polite" className="sr-only">
        {copied ? t("copied") : ""}
      </span>
    </button>
  );
}
