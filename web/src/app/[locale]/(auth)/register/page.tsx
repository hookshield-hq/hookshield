import { Suspense } from "react";
import type { Metadata } from "next";
import { getTranslations } from "next-intl/server";
import { RegisterForm } from "@/components/auth/register-form";
import { Skeleton } from "@/components/ui/skeleton";

export async function generateMetadata({ params }: PageProps<"/[locale]/register">): Promise<Metadata> {
  const { locale } = await params;
  const t = await getTranslations({ locale, namespace: "auth" });
  return { title: t("register.title") };
}

export default function Page() {
  // The form reads the query string (next, token, error): render it on the client.
  return (
    <Suspense fallback={<Skeleton className="h-80 w-full" />}>
      <RegisterForm />
    </Suspense>
  );
}
