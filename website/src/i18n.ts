import en from "../messages/en.json";
import ptBR from "../messages/pt-BR.json";
import es from "../messages/es.json";

// The landing page exists in three languages (guide D7): English at the site
// root, the others under /pt-BR/ and /es/. The docs are English only (D10).
export const locales = ["en", "pt-BR", "es"] as const;
export type Locale = (typeof locales)[number];
export const defaultLocale: Locale = "en";

/** Locales that live under their own path prefix (English is the root). */
export const prefixedLocales = locales.filter((l) => l !== defaultLocale);

export const localeNames: Record<Locale, string> = {
  en: "English",
  "pt-BR": "Português",
  es: "Español",
};

export type Messages = typeof en;
const catalogs: Record<Locale, Messages> = { en, "pt-BR": ptBR, es };

export function isLocale(value: string): value is Locale {
  return (locales as readonly string[]).includes(value);
}

/** Root of a locale's landing page: "/", "/pt-BR/", "/es/". */
export function localeRoot(locale: Locale): string {
  return locale === defaultLocale ? "/" : `/${locale}/`;
}

type Vars = Record<string, string | number>;

/**
 * Translator for one locale. Keys are dot paths ("hero.title"); {name}
 * placeholders are replaced from vars. A missing key throws at build time, so a
 * typo never ships as raw text.
 */
export function getT(locale: Locale) {
  const messages = catalogs[locale];
  return function t(key: string, vars?: Vars): string {
    let node: unknown = messages;
    for (const part of key.split(".")) {
      node = node && typeof node === "object" ? (node as Record<string, unknown>)[part] : undefined;
    }
    if (typeof node !== "string") throw new Error(`missing message "${key}" (${locale})`);
    if (!vars) return node;
    return node.replace(/\{(\w+)\}/g, (match, name: string) => (name in vars ? String(vars[name]) : match));
  };
}
export type T = ReturnType<typeof getT>;

/** Number formatting per locale (5,000 · 5.000 · 5000). */
export function formatNumber(locale: Locale, n: number): string {
  return new Intl.NumberFormat(locale).format(n);
}

/** USD price per locale ($29 · US$ 29 · 29 US$). Cents are shown only when needed. */
export function formatUSD(locale: Locale, cents: number): string {
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: cents % 100 === 0 ? 0 : 2,
  }).format(cents / 100);
}
