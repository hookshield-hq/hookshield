import { Suspense } from "react";
import type { Metadata } from "next";
import { getTranslations } from "next-intl/server";
import { ForgotPasswordForm } from "@/components/auth/recovery";
import { Skeleton } from "@/components/ui/skeleton";

export async function generateMetadata({ params }: PageProps<"/[locale]/forgot-password">): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "auth" });
  return { title: t("forgot.title") };
}

export default function Page() {
  // The form reads the query string (next, token, error): render it on the client.
  return (
    <Suspense fallback={<Skeleton className="h-80 w-full" />}>
      <ForgotPasswordForm />
    </Suspense>
  );
}
