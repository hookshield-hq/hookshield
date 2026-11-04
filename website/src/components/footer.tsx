import { APP_URL, GITHUB_URL } from "@/site";
import { getT, localeRoot, type Locale } from "@/i18n";
import { Logo } from "./logo";

export function Footer({ locale }: { locale: Locale }) {
  const t = getT(locale);
  const home = localeRoot(locale);
  const columns = [
    {
      title: t("footer.product"),
      links: [
        { href: `${home}#features`, label: t("nav.features") },
        { href: `${home}#pricing`, label: t("nav.pricing") },
        { href: `${home}#self-host`, label: t("nav.selfHost") },
        { href: `${APP_URL}/${locale}/login`, label: t("nav.signIn") },
      ],
    },
    {
      title: t("footer.resources"),
      links: [
        { href: "/docs/", label: t("nav.docs") },
        { href: GITHUB_URL, label: t("nav.github") },
        { href: `${GITHUB_URL}/releases`, label: t("footer.changelog") },
        { href: "/docs/security/", label: t("footer.security") },
      ],
    },
    {
      title: t("footer.legal"),
      links: [
        { href: "/legal/terms/", label: t("footer.terms") },
        { href: "/legal/privacy/", label: t("footer.privacy") },
        { href: "/legal/refund/", label: t("footer.refund") },
        { href: `${GITHUB_URL}/blob/main/LICENSE`, label: t("footer.license") },
      ],
    },
  ];
  return (
    <footer className="border-t border-line">
      <div className="mx-auto grid max-w-6xl gap-10 px-5 py-14 sm:grid-cols-2 lg:grid-cols-5">
        <div className="lg:col-span-2">
          <Logo className="h-11 w-auto" />
          <p className="mt-3 max-w-xs text-sm text-muted">{t("footer.tagline")}</p>
        </div>
        {columns.map((c) => (
          <nav key={c.title} aria-label={c.title}>
            <h2 className="text-sm font-semibold">{c.title}</h2>
            <ul className="mt-3 space-y-2 text-sm">
              {c.links.map((l) => (
                <li key={l.href}>
                  <a href={l.href} className="text-muted transition hover:text-fg">
                    {l.label}
                  </a>
                </li>
              ))}
            </ul>
          </nav>
        ))}
      </div>
      <p className="mx-auto max-w-6xl px-5 pb-10 text-xs text-subtle">{t("footer.copyright", { year: new Date().getFullYear() })}</p>
    </footer>
  );
}
