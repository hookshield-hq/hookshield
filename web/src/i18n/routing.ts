import { defineRouting } from "next-intl/routing";

// Every visible label exists in these three languages (guide D7).
export const locales = ["en", "pt-BR", "es"] as const;
export type Locale = (typeof locales)[number];

export const routing = defineRouting({
  locales,
  defaultLocale: "en",
  localePrefix: "always",
});
