import { DocView } from "@/components/doc-view";
import { findDoc, renderDoc } from "@/lib/docs";
import { pageMetadata } from "@/lib/metadata";

export async function generateMetadata() {
  const doc = await renderDoc(findDoc("")!);
  return pageMetadata("Documentation", doc.description, "/docs/");
}

export default async function DocsHome() {
  return <DocView doc={await renderDoc(findDoc("")!)} />;
}
