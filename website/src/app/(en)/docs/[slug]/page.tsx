import { notFound } from "next/navigation";
import { DocView } from "@/components/doc-view";
import { allDocs, docHref, findDoc, renderDoc } from "@/lib/docs";
import { pageMetadata } from "@/lib/metadata";

export const dynamicParams = false;

export function generateStaticParams() {
  return allDocs.filter((d) => d.slug).map((d) => ({ slug: d.slug }));
}

export async function generateMetadata({ params }: PageProps<"/docs/[slug]">) {
  const page = findDoc((await params).slug);
  if (!page) notFound();
  const doc = await renderDoc(page);
  return pageMetadata(doc.title, doc.description, docHref(page.slug));
}

export default async function DocPage({ params }: PageProps<"/docs/[slug]">) {
  const page = findDoc((await params).slug);
  if (!page) notFound();
  return <DocView doc={await renderDoc(page)} />;
}
