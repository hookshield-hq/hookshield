import { notFound } from "next/navigation";
import { TriangleAlert } from "lucide-react";
import { Footer } from "@/components/footer";
import { Header } from "@/components/header";
import { getT } from "@/i18n";
import { legalSlugs, loadLegal, type LegalSlug } from "@/lib/legal";
import { pageMetadata } from "@/lib/metadata";

export const dynamicParams = false;

export function generateStaticParams() {
  return legalSlugs.map((slug) => ({ slug }));
}

async function load(slug: string) {
  if (!(legalSlugs as readonly string[]).includes(slug)) notFound();
  return loadLegal(slug as LegalSlug);
}

export async function generateMetadata({ params }: PageProps<"/legal/[slug]">) {
  const page = await load((await params).slug);
  return {
    ...pageMetadata(page.title, page.description, `/legal/${page.slug}/`),
    ...(page.draft ? { robots: { index: false, follow: true } } : {}),
  };
}

export default async function LegalPage({ params }: PageProps<"/legal/[slug]">) {
  const page = await load((await params).slug);
  const t = getT("en");
  return (
    <>
      <Header locale="en" current="legal" />
      <main id="content" className="mx-auto max-w-3xl px-5 py-14">
        {page.draft && (
          <p role="note" className="mb-8 flex gap-2 rounded-xl border border-line bg-warning-bg p-4 text-sm">
            <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden />
            {t("legal.draft")}
          </p>
        )}
        <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">{page.title}</h1>
        <p className="mt-2 text-sm text-subtle">{t("legal.updated", { date: page.updated })}</p>
        <div className="prose mt-8" dangerouslySetInnerHTML={{ __html: page.html }} />
      </main>
      <Footer locale="en" />
    </>
  );
}
