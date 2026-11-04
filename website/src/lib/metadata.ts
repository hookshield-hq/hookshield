import type { Metadata } from "next";
import { SITE_URL } from "@/site";
import { getT, localeRoot, locales, type Locale } from "@/i18n";

const ogLocale: Record<Locale, string> = { en: "en_US", "pt-BR": "pt_BR", es: "es_ES" };

/** Shared defaults: absolute URLs, icons, social image. */
export const baseMetadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  icons: { icon: "/brand/icon.png", apple: "/brand/apple-icon.png" },
  openGraph: {
    siteName: "HookShield",
    type: "website",
    images: [{ url: "/brand/opengraph-image.png", width: 1200, height: 630, alt: "HookShield" }],
  },
  twitter: { card: "summary_large_image" },
};

/** Metadata of a landing page, with hreflang links to the other languages. */
export function landingMetadata(locale: Locale): Metadata {
  const t = getT(locale);
  const languages = Object.fromEntries(locales.map((l) => [l, localeRoot(l)]));
  return {
    ...baseMetadata,
    title: t("meta.title"),
    description: t("meta.description"),
    alternates: { canonical: localeRoot(locale), languages: { ...languages, "x-default": "/" } },
    openGraph: {
      ...baseMetadata.openGraph,
      title: t("meta.title"),
      description: t("meta.description"),
      url: localeRoot(locale),
      locale: ogLocale[locale],
    },
  };
}

/** Metadata of an English-only page (docs, legal). */
export function pageMetadata(title: string, description: string, path: string): Metadata {
  return {
    ...baseMetadata,
    title: `${title} · HookShield`,
    description,
    alternates: { canonical: path },
    openGraph: { ...baseMetadata.openGraph, title: `${title} · HookShield`, description, url: path, locale: ogLocale.en },
  };
}
