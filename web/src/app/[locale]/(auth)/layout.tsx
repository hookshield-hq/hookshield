import { useTranslations } from "next-intl";
import { ShieldCheck } from "lucide-react";
import { Logo } from "@/components/shell/logo";
import { LocaleSwitcher, ThemeSwitcher } from "@/components/shell/preferences";

export default function AuthLayout({ children }: LayoutProps<"/[locale]">) {
  const t = useTranslations("auth");
  return (
    <div className="grid min-h-dvh lg:grid-cols-2">
      <aside className="relative hidden overflow-hidden border-r border-line lg:flex lg:flex-col lg:justify-between lg:p-12">
        <div className="pointer-events-none absolute -left-32 -top-32 size-[28rem] rounded-full bg-accent-from/20 blur-3xl" />
        <div className="pointer-events-none absolute -bottom-40 right-0 size-[28rem] rounded-full bg-accent-to/15 blur-3xl" />
        <Logo className="relative h-16 -ml-3" />
        <div className="relative max-w-md">
          <ShieldCheck className="mb-6 size-10 text-accent" />
          <p className="text-accent-gradient text-3xl font-semibold tracking-tight">{t("tagline")}</p>
          <p className="mt-4 text-base text-muted">{t("taglineBody")}</p>
        </div>
        <div />
      </aside>
      <div className="flex flex-col">
        <div className="flex items-center justify-between p-4">
          <Logo className="h-10 lg:invisible" />
          <div className="flex items-center gap-1">
            <LocaleSwitcher />
            <ThemeSwitcher />
          </div>
        </div>
        <main className="flex flex-1 items-center justify-center px-4 pb-16">
          <div className="w-full max-w-sm">{children}</div>
        </main>
      </div>
    </div>
  );
}
