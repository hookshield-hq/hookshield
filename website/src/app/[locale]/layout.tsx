import { notFound } from "next/navigation";
import { RootShell } from "@/components/root-shell";
import { isLocale, prefixedLocales } from "@/i18n";

// Root layout of the translated landing pages (/pt-BR/, /es/).
export const dynamicParams = false;

export function generateStaticParams() {
  return prefixedLocales.map((locale) => ({ locale }));
}

export default async function LocaleLayout({ children, params }: LayoutProps<"/[locale]">) {
  const { locale } = await params;
  if (!isLocale(locale) || locale === "en") notFound();
  return <RootShell locale={locale}>{children}</RootShell>;
}
