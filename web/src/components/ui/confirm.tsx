"use client";

import type { ReactNode } from "react";
import { useTranslations } from "next-intl";
import { Button } from "./button";
import { GlassModal } from "./overlay";

/** Asks before something that cannot be undone (delete, revoke, rotate). */
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  body,
  confirmLabel,
  loading,
  tone = "danger",
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: ReactNode;
  body: ReactNode;
  confirmLabel: ReactNode;
  loading?: boolean;
  tone?: "danger" | "primary";
}) {
  const t = useTranslations("common");
  return (
    <GlassModal
      open={open}
      onClose={onClose}
      title={title}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("cancel")}
          </Button>
          <Button variant={tone} loading={loading} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="text-sm text-muted">{body}</div>
    </GlassModal>
  );
}
