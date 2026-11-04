import { Menu } from "lucide-react";
import Link from "next/link";
import { APP_URL, GITHUB_URL } from "@/site";
import { getT, localeRoot, type Locale } from "@/i18n";
import { Logo } from "./logo";
import { ThemeToggle } from "./theme-toggle";
import { LanguageMenu } from "./language-menu";

/** Sticky top bar of every page. On docs pages the section links go back to the landing page. */
export function Header({ locale, current }: { locale: Locale; current: "home" | "docs" | "legal" }) {
  const t = getT(locale);
  const home = localeRoot(locale);
  const links = [
    { href: `${home}#features`, label: t("nav.features") },
    { href: `${home}#self-host`, label: t("nav.selfHost") },
    { href: `${home}#pricing`, label: t("nav.pricing") },
    { href: "/docs/", label: t("nav.docs"), active: current === "docs" },
    { href: GITHUB_URL, label: t("nav.github"), external: true },
  ];
  return (
    <header className="sticky top-0 z-40 border-b border-line bg-bg/80 backdrop-blur-md">
      <div className="mx-auto flex h-16 max-w-6xl items-center gap-6 px-5">
        <Link href={home} aria-label={t("nav.home")} className="shrink-0">
          <Logo />
        </Link>
        <nav aria-label={t("nav.menu")} className="hidden items-center gap-1 text-sm md:flex">
          {links.map((l) => (
            <a
              key={l.href}
              href={l.href}
              {...(l.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
              aria-current={l.active ? "page" : undefined}
              className={`rounded-lg px-3 py-2 transition hover:bg-hover hover:text-fg ${l.active ? "text-fg" : "text-muted"}`}
            >
              {l.label}
            </a>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-1">
          <LanguageMenu locale={locale} label={t("nav.language")} />
          <ThemeToggle label={t("nav.theme")} />
          <a href={`${APP_URL}/${locale}/login`} className="hidden rounded-lg px-3 py-2 text-sm text-muted transition hover:bg-hover hover:text-fg sm:inline-block">
            {t("nav.signIn")}
          </a>
          <a
            href={`${APP_URL}/${locale}/register`}
            className="bg-accent-gradient hidden rounded-lg px-3.5 py-2 text-sm font-medium text-white shadow-sm transition hover:opacity-90 sm:inline-block"
          >
            {t("nav.startFree")}
          </a>
          {/* Small screens: the same links in a disclosure, no JavaScript needed. */}
          <details className="group relative md:hidden">
            <summary
              aria-label={t("nav.menu")}
              className="inline-flex size-9 cursor-pointer list-none items-center justify-center rounded-lg text-muted hover:bg-hover hover:text-fg [&::-webkit-details-marker]:hidden"
            >
              <Menu className="size-5" aria-hidden />
            </summary>
            <div className="panel absolute right-0 mt-2 flex w-56 flex-col rounded-xl p-2 text-sm">
              {links.map((l) => (
                <a key={l.href} href={l.href} className="rounded-lg px-3 py-2 text-muted hover:bg-hover hover:text-fg">
                  {l.label}
                </a>
              ))}
              <a href={`${APP_URL}/${locale}/login`} className="rounded-lg px-3 py-2 text-muted hover:bg-hover hover:text-fg">
                {t("nav.signIn")}
              </a>
              <a href={`${APP_URL}/${locale}/register`} className="bg-accent-gradient mt-1 rounded-lg px-3 py-2 text-center font-medium text-white">
                {t("nav.startFree")}
              </a>
            </div>
          </details>
        </div>
      </div>
    </header>
  );
}
