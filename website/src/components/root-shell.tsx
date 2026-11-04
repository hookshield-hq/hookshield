import { GeistSans } from "geist/font/sans";
import { GeistMono } from "geist/font/mono";
import { getT, type Locale } from "@/i18n";
import { Providers } from "./providers";
import "../app/globals.css";

/** <html> and <body> of every page (the site has one root layout per language group). */
export function RootShell({ locale, children }: { locale: Locale; children: React.ReactNode }) {
  const t = getT(locale);
  return (
    <html lang={locale} suppressHydrationWarning className={`${GeistSans.variable} ${GeistMono.variable} dark`}>
      <body className="min-h-dvh">
        <a
          href="#content"
          className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-lg focus:bg-surface-strong focus:px-4 focus:py-2"
        >
          {t("nav.skip")}
        </a>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
