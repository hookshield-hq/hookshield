"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { AnimatePresence, motion, useDragControls } from "motion/react";
import { X } from "lucide-react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/cn";

/**
 * Locks page scroll, closes on Escape (unless not dismissible) and returns
 * focus when the overlay closes.
 */
function useOverlay(open: boolean, onClose: () => void, panel: React.RefObject<HTMLDivElement | null>, dismissible = true) {
  const latest = useRef({ onClose, dismissible });
  useEffect(() => {
    latest.current = { onClose, dismissible };
  }, [onClose, dismissible]);
  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement as HTMLElement | null;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const key = (e: KeyboardEvent) => e.key === "Escape" && latest.current.dismissible && latest.current.onClose();
    document.addEventListener("keydown", key);
    requestAnimationFrame(() => panel.current?.focus());
    return () => {
      document.body.style.overflow = overflow;
      document.removeEventListener("keydown", key);
      previous?.focus?.();
    };
  }, [open, panel]);
}

function CloseButton({ onClose }: { onClose: () => void }) {
  const t = useTranslations("common");
  return (
    <button
      type="button"
      onClick={onClose}
      aria-label={t("close")}
      className="inline-flex size-8 items-center justify-center rounded-md text-muted transition hover:bg-hover hover:text-fg"
    >
      <X className="size-4" />
    </button>
  );
}

/** Side panel (log debugger, create endpoint). */
export function Drawer({
  open,
  onClose,
  title,
  children,
  footer,
  width = "max-w-2xl",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  width?: string;
}) {
  const panel = useRef<HTMLDivElement>(null);
  useOverlay(open, onClose, panel);
  if (typeof document === "undefined") return null;
  return createPortal(
    <AnimatePresence>
      {open && (
        <div className="fixed inset-0 z-50">
          <motion.div
            className="absolute inset-0 bg-overlay backdrop-blur-sm"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            onClick={onClose}
          />
          <motion.div
            ref={panel}
            role="dialog"
            aria-modal="true"
            tabIndex={-1}
            className={cn("panel absolute inset-y-0 right-0 flex w-full flex-col outline-none", width)}
            initial={{ x: "100%" }}
            animate={{ x: 0 }}
            exit={{ x: "100%" }}
            transition={{ duration: 0.15, ease: "easeOut" }}
          >
            <div className="flex items-center justify-between gap-4 border-b border-line px-5 py-4">
              <h2 className="text-base font-semibold text-fg">{title}</h2>
              <CloseButton onClose={onClose} />
            </div>
            <div className="flex-1 overflow-y-auto p-5">{children}</div>
            {footer && <div className="flex justify-end gap-2 border-t border-line px-5 py-4">{footer}</div>}
          </motion.div>
        </div>
      )}
    </AnimatePresence>,
    document.body,
  );
}

/**
 * Centered glass modal; on touch devices it can be dragged down to close (blueprint §6).
 * With dismissible={false} (e.g. while a one-time secret is unacknowledged) Escape,
 * the backdrop, the close button and dragging do not close it: only the footer actions do.
 */
export function GlassModal({
  open,
  onClose,
  title,
  children,
  footer,
  dismissible = true,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  dismissible?: boolean;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const drag = useDragControls();
  useOverlay(open, onClose, panel, dismissible);
  if (typeof document === "undefined") return null;
  return createPortal(
    <AnimatePresence>
      {open && (
        <div className="fixed inset-0 z-50 flex items-end justify-center p-4 sm:items-center">
          <motion.div
            className="absolute inset-0 bg-overlay backdrop-blur-sm"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            onClick={dismissible ? onClose : undefined}
          />
          <motion.div
            ref={panel}
            role="dialog"
            aria-modal="true"
            tabIndex={-1}
            drag={dismissible ? "y" : false}
            dragControls={drag}
            dragListener={false}
            dragConstraints={{ top: 0, bottom: 0 }}
            dragElastic={{ top: 0, bottom: 0.6 }}
            onDragEnd={(_, info) => dismissible && info.offset.y > 120 && onClose()}
            className="panel relative w-full max-w-lg rounded-2xl outline-none"
            initial={{ opacity: 0, y: 24, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 24, scale: 0.98 }}
            transition={{ duration: 0.15, ease: "easeOut" }}
          >
            <div
              className={cn("flex items-center justify-between gap-4 border-b border-line px-5 py-4", dismissible && "cursor-grab active:cursor-grabbing")}
              onPointerDown={dismissible ? (e) => drag.start(e) : undefined}
            >
              <h2 className="text-base font-semibold text-fg">{title}</h2>
              {dismissible && <CloseButton onClose={onClose} />}
            </div>
            <div className="p-5">{children}</div>
            {footer && <div className="flex justify-end gap-2 border-t border-line px-5 py-4">{footer}</div>}
          </motion.div>
        </div>
      )}
    </AnimatePresence>,
    document.body,
  );
}
