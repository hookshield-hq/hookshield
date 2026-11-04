"use client";

import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import { AlertCircle, CheckCircle2, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/cn";

type Toast = { id: number; tone: "success" | "error"; message: string };
type Ctx = { success: (message: string) => void; error: (message: string) => void };

const ToastContext = createContext<Ctx | null>(null);

/** Short confirmations ("Endpoint saved") and errors that have no field to sit next to. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const next = useRef(0);
  const dismiss = useCallback((id: number) => setToasts((ts) => ts.filter((t) => t.id !== id)), []);
  const push = useCallback(
    (tone: Toast["tone"], message: string) => {
      const id = ++next.current;
      setToasts((ts) => [...ts.slice(-2), { id, tone, message }]);
      setTimeout(() => dismiss(id), tone === "error" ? 6000 : 3500);
    },
    [dismiss],
  );
  const value = useMemo<Ctx>(() => ({ success: (m) => push("success", m), error: (m) => push("error", m) }), [push]);
  return (
    <ToastContext.Provider value={value}>
      {children}
      <ToastViewport toasts={toasts} dismiss={dismiss} />
    </ToastContext.Provider>
  );
}

function ToastViewport({ toasts, dismiss }: { toasts: Toast[]; dismiss: (id: number) => void }) {
  const t = useTranslations("common");
  return (
    <div aria-live="polite" className="pointer-events-none fixed top-16 right-4 z-[60] flex w-full max-w-sm flex-col gap-2">
      <AnimatePresence initial={false}>
        {toasts.map((toast) => (
          <motion.div
            key={toast.id}
            layout
            initial={{ opacity: 0, y: -12, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, scale: 0.98 }}
            transition={{ duration: 0.15, ease: "easeOut" }}
            role={toast.tone === "error" ? "alert" : "status"}
            className="panel pointer-events-auto flex items-start gap-3 rounded-xl px-4 py-3 text-sm text-fg"
          >
            {toast.tone === "success" ? (
              <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-success" />
            ) : (
              <AlertCircle className="mt-0.5 size-4 shrink-0 text-danger" />
            )}
            <p className="flex-1">{toast.message}</p>
            <button
              type="button"
              onClick={() => dismiss(toast.id)}
              aria-label={t("close")}
              className={cn("-m-1 rounded p-1 text-subtle transition hover:text-fg")}
            >
              <X className="size-3.5" />
            </button>
          </motion.div>
        ))}
      </AnimatePresence>
    </div>
  );
}

export function useToast(): Ctx {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast outside ToastProvider");
  return ctx;
}
