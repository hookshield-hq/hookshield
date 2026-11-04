"use client";

import { Fragment, useCallback, useEffect, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import { Menu, Search } from "lucide-react";
import { useTranslations } from "next-intl";
import { usePathname, useRouter } from "@/i18n/navigation";
import { useMe } from "@/lib/auth";
import { useErrorMessage } from "@/lib/use-error-message";
import { useResetOnHide } from "@/lib/use-reset-on-hide";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Kbd } from "@/components/ui/kbd";
import { Skeleton } from "@/components/ui/skeleton";
import { ClusterStatus } from "./cluster-status";
import { CommandPalette, useCommandPaletteShortcut } from "./command-palette";
import { LocaleSwitcher, ThemeSwitcher } from "./preferences";
import { Sidebar } from "./sidebar";
import { UserMenu } from "./user-menu";

/** Authenticated layout: sidebar, global header, ⌘K palette. Signed-out users go to /login. */
export function AppShell({ children }: { children: ReactNode }) {
  const t = useTranslations();
  const me = useMe();
  const router = useRouter();
  const pathname = usePathname();
  const errorMessage = useErrorMessage();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [mobileNav, setMobileNav] = useState(false);
  useCommandPaletteShortcut(setPaletteOpen);
  const closeMobile = useCallback(() => setMobileNav(false), []);
  useResetOnHide(() => {
    setPaletteOpen(false);
    setMobileNav(false);
  });

  useEffect(() => {
    if (me.data === null) router.replace(`/login?next=${encodeURIComponent(pathname)}`);
  }, [me.data, pathname, router]);

  if (me.isError) {
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        <EmptyState
          title={t("common.errorTitle")}
          body={errorMessage(me.error)}
          action={<Button onClick={() => me.refetch()}>{t("common.retry")}</Button>}
        />
      </main>
    );
  }
  if (!me.data) return <ShellSkeleton />;

  return (
    <div className="flex min-h-dvh">
      <a href="#content" className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-lg focus:bg-surface-strong focus:px-4 focus:py-2">
        {t("common.skipToContent")}
      </a>
      <Sidebar className="sticky top-0 hidden h-dvh md:flex" />

      <AnimatePresence>
        {mobileNav && (
          <div className="fixed inset-0 z-40 md:hidden">
            <motion.div className="absolute inset-0 bg-overlay" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} onClick={closeMobile} />
            <motion.div className="absolute inset-y-0 left-0" initial={{ x: "-100%" }} animate={{ x: 0 }} exit={{ x: "-100%" }} transition={{ duration: 0.15, ease: "easeOut" }}>
              <Sidebar onNavigate={closeMobile} className="h-full bg-surface-strong" />
            </motion.div>
          </div>
        )}
      </AnimatePresence>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-line bg-bg/80 px-4 backdrop-blur-md">
          <Button variant="ghost" size="icon" className="md:hidden" aria-label={t("nav.openMenu")} onClick={() => setMobileNav(true)}>
            <Menu className="size-4" />
          </Button>
          <button
            type="button"
            onClick={() => setPaletteOpen(true)}
            // The visible text is the accessible name (WCAG 2.5.3); the purpose goes in the tooltip.
            title={t("shell.searchShortcut")}
            aria-haspopup="dialog"
            aria-keyshortcuts="Control+K Meta+K"
            className="glass flex h-9 w-full max-w-md items-center gap-2 rounded-lg px-3 text-sm text-subtle transition hover:text-muted"
          >
            <Search className="size-4" />
            <span className="flex-1 truncate text-left">{t("shell.search")}</span>
            <span className="hidden items-center gap-0.5 sm:flex">
              <Kbd>⌘</Kbd>
              <Kbd>K</Kbd>
            </span>
          </button>
          <div className="ml-auto flex items-center gap-1">
            <ClusterStatus />
            <div className="mx-2 hidden h-5 w-px bg-line lg:block" />
            <LocaleSwitcher />
            <ThemeSwitcher />
            <div className="ml-1">
              <UserMenu user={me.data} />
            </div>
          </div>
        </header>
        <main id="content" className="flex-1 p-4 md:p-8">
          {/* Keyed by user: nothing of a previous account survives a sign-out + sign-in. */}
          <Fragment key={me.data.id}>{children}</Fragment>
        </main>
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  );
}

/** Placeholder while the session loads (also the Suspense fallback of the (app) layout). */
export function ShellSkeleton() {
  const t = useTranslations("common");
  return (
    <div className="flex min-h-dvh" aria-busy="true">
      <div className="hidden w-60 border-r border-line p-4 md:block">
        <Skeleton className="h-7 w-32" />
      </div>
      <div className="flex-1 p-6">
        <Skeleton className="mb-6 h-8 w-64" />
        <Skeleton className="h-40 w-full" />
      </div>
      <span className="sr-only">{t("loading")}</span>
    </div>
  );
}
