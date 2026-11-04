"use client";

import { useRef, useState } from "react";
import { useTranslations } from "next-intl";
import { LogOut, Settings } from "lucide-react";
import { Link, useRouter } from "@/i18n/navigation";
import { useLogout, type User } from "@/lib/auth";
import { useDismiss } from "@/components/ui/use-dismiss";
import { useResetOnHide } from "@/lib/use-reset-on-hide";
import { Badge } from "@/components/ui/badge";
import { useToast } from "@/components/ui/toast";

function initials(u: User): string {
  const source = u.name?.trim() || u.email;
  const parts = source.split(/[\s@._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? "")).toUpperCase() || "?";
}

export function UserMenu({ user }: { user: User }) {
  const t = useTranslations("shell.user");
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(open, ref, () => setOpen(false));
  useResetOnHide(() => setOpen(false));
  const logout = useLogout();
  const router = useRouter();
  const toast = useToast();

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label={t("menu")}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="bg-accent-gradient inline-flex size-8 items-center justify-center rounded-full text-xs font-semibold text-white"
      >
        {initials(user)}
      </button>
      {open && (
        <div role="menu" className="panel absolute right-0 z-40 mt-2 w-64 rounded-xl p-1">
          <div className="border-b border-line px-3 py-2.5">
            {user.name && <p className="truncate text-sm font-medium text-fg">{user.name}</p>}
            <p className="truncate text-xs text-muted">{user.email}</p>
            {!user.email_verified && (
              <Badge tone="warning" className="mt-2">
                {t("unverified")}
              </Badge>
            )}
          </div>
          <Link
            href="/settings"
            role="menuitem"
            onClick={() => setOpen(false)}
            className="mt-1 flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-fg transition hover:bg-hover"
          >
            <Settings className="size-4 text-muted" />
            {t("settings")}
          </Link>
          <button
            type="button"
            role="menuitem"
            onClick={() => logout.mutate(undefined, { onSuccess: () => router.replace("/login"), onError: () => toast.error(t("logoutFailed")) })}
            className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-fg transition hover:bg-hover"
          >
            <LogOut className="size-4 text-muted" />
            {t("logout")}
          </button>
        </div>
      )}
    </div>
  );
}
