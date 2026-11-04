import { Suspense } from "react";
import type { Metadata } from "next";
import { getTranslations } from "next-intl/server";
import { EndpointDetail } from "@/components/endpoints/endpoint-detail";
import { Skeleton } from "@/components/ui/skeleton";

// The authenticated shell renders this page only after the session is loaded
// in the browser (the API owns the session cookie), so it cannot be validated
// for instant navigation on the server.
export const instant = false;

export async function generateMetadata({ params }: PageProps<"/[locale]/endpoints/[id]">): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "nav" });
  return { title: t("endpoints") };
}

async function Detail({ params }: { params: PageProps<"/[locale]/endpoints/[id]">["params"] }) {
  const { id } = await params;
  return <EndpointDetail id={id} />;
}

export default function Page({ params }: PageProps<"/[locale]/endpoints/[id]">) {
  // The endpoint ID is only known at request time: stream it behind a skeleton.
  return (
    <Suspense fallback={<Skeleton className="mx-auto h-64 max-w-6xl" />}>
      <Detail params={params} />
    </Suspense>
  );
}
