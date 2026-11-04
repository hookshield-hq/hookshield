import type { MetadataRoute } from "next";
import { SITE_URL } from "@/site";
import { localeRoot, locales } from "@/i18n";
import { allDocs, docHref } from "@/lib/docs";
import { legalSlugs, loadLegal } from "@/lib/legal";

export const dynamic = "force-static";

export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const languages = {
    ...Object.fromEntries(locales.map((l) => [l, `${SITE_URL}${localeRoot(l)}`])),
    "x-default": `${SITE_URL}/`,
  };
  // Draft legal pages are not indexed (noindex), so they stay out of the sitemap too.
  const legal = (await Promise.all(legalSlugs.map(loadLegal))).filter((p) => !p.draft);
  return [
    ...locales.map((l) => ({ url: `${SITE_URL}${localeRoot(l)}`, priority: 1, alternates: { languages } })),
    ...allDocs.map((d) => ({ url: `${SITE_URL}${docHref(d.slug)}`, priority: d.slug ? 0.7 : 0.9 })),
    ...legal.map((p) => ({ url: `${SITE_URL}/legal/${p.slug}/`, priority: 0.3 })),
  ];
}
