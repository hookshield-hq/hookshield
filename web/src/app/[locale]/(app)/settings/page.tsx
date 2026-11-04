import type { Metadata } from "next";
import { getTranslations } from "next-intl/server";
import { SettingsPage } from "@/components/settings/settings-page";

// The authenticated shell renders this page only after the session is loaded
// in the browser (the API owns the session cookie), so it cannot be validated
// for instant navigation on the server.
export const instant = false;

export async function generateMetadata({ params }: PageProps<"/[locale]/settings">): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "nav" });
  return { title: t("settings") };
}

export default function Page() {
  return <SettingsPage />;
}
