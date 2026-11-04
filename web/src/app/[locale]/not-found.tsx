import { useTranslations } from "next-intl";
import { Compass } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { EmptyState } from "@/components/ui/empty-state";

export default function NotFound() {
  const t = useTranslations("common");
  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <EmptyState
        icon={<Compass className="size-6" />}
        title={t("notFoundTitle")}
        body={t("notFoundBody")}
        action={
          <Link href="/" className="bg-accent-gradient inline-flex h-9 items-center rounded-lg px-4 text-sm font-medium text-white">
            {t("goHome")}
          </Link>
        }
      />
    </main>
  );
}
