"use client";

import { useTranslations } from "next-intl";
import { BookOpen, ExternalLink } from "lucide-react";
import { Link, usePathname } from "@/i18n/navigation";
import { cn } from "@/lib/cn";
import { useFeatures } from "@/lib/queries";
import { DOCS_URL, isActive, visibleNav } from "./nav";
import { Logo } from "./logo";

export function Sidebar({ onNavigate, className }: { onNavigate?: () => void; className?: string }) {
  const t = useTranslations("nav");
  const pathname = usePathname();
  const features = useFeatures();
  return (
    <aside className={cn("flex w-60 flex-col border-r border-line bg-surface backdrop-blur-md", className)}>
      <div className="flex h-14 items-center border-b border-line px-4">
        <Link href="/" onClick={onNavigate} className="-ml-2 h-11">
          <Logo className="h-11" />
        </Link>
      </div>
      <nav aria-label={t("main")} className="flex-1 overflow-y-auto p-2">
        <ul className="flex flex-col gap-0.5">
          {visibleNav(features.data).map((item) => {
            const active = isActive(pathname, item.href);
            const Icon = item.icon;
            return (
              <li key={item.href}>
                <Link
                  href={item.href}
                  onClick={onNavigate}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "group relative flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition",
                    active ? "bg-hover text-fg" : "text-muted hover:bg-hover hover:text-fg",
                  )}
                >
                  {active && <span className="bg-accent-gradient absolute inset-y-1.5 left-0 w-0.5 rounded-full" />}
                  <Icon className={cn("size-4 shrink-0", active ? "text-accent" : "text-subtle group-hover:text-fg")} />
                  <span className="truncate">{t(item.key)}</span>
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>
      <div className="border-t border-line p-2">
        <a
          href={DOCS_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="group flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted transition hover:bg-hover hover:text-fg"
        >
          <BookOpen className="size-4 shrink-0 text-subtle group-hover:text-fg" />
          <span className="truncate">{t("docs")}</span>
          <ExternalLink className="ml-auto size-3.5 text-subtle" aria-hidden />
        </a>
      </div>
    </aside>
  );
}
