import { Suspense } from "react";
import type { Metadata } from "next";
import { getTranslations } from "next-intl/server";
import { SandboxPage } from "@/components/sandbox/sandbox-page";
import { Skeleton } from "@/components/ui/skeleton";

// The authenticated shell renders this page only after the session is loaded
// in the browser (the API owns the session cookie), so it cannot be validated
// for instant navigation on the server.
export const instant = false;

export async function generateMetadata({ params }: PageProps<"/[locale]/sandbox">): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "nav" });
  return { title: t("sandbox") };
}

export default function Page() {
  return (
    <Suspense fallback={<Skeleton className="mx-auto h-96 max-w-7xl" />}>
      <SandboxPage />
    </Suspense>
  );
}
