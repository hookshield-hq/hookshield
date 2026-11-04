import { notFound } from "next/navigation";
import { Footer } from "@/components/footer";
import { Header } from "@/components/header";
import { Landing } from "@/components/landing";
import { isLocale } from "@/i18n";
import { landingMetadata } from "@/lib/metadata";

export async function generateMetadata({ params }: PageProps<"/[locale]">) {
  const { locale } = await params;
  if (!isLocale(locale)) notFound();
  return landingMetadata(locale);
}

export default async function LocalizedHome({ params }: PageProps<"/[locale]">) {
  const { locale } = await params;
  if (!isLocale(locale)) notFound();
  return (
    <>
      <Header locale={locale} current="home" />
      <Landing locale={locale} />
      <Footer locale={locale} />
    </>
  );
}
