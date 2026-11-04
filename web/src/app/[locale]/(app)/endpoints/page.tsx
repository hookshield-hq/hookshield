import type { Metadata } from "next";
import { getTranslations } from "next-intl/server";
import { EndpointList } from "@/components/endpoints/endpoint-list";

// The authenticated shell renders this page only after the session is loaded
// in the browser (the API owns the session cookie), so it cannot be validated
// for instant navigation on the server.
export const instant = false;

export async function generateMetadata({ params }: PageProps<"/[locale]/endpoints">): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "nav" });
  return { title: t("endpoints") };
}

export default function Page() {
  return <EndpointList />;
}
