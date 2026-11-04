import Image from "next/image";
import { useTranslations } from "next-intl";
import brandDark from "@/assets/brand-to-dark.webp";
import brandLight from "@/assets/brand-to-light.webp";
import icon from "@/assets/only-icon.webp";
import { cn } from "@/lib/cn";

/** Full logo: light text on the dark theme, dark text on the light theme (guide §7.3). */
export function Logo({ className }: { className?: string }) {
  const t = useTranslations("common");
  return (
    <span className={cn("relative inline-flex items-center", className)}>
      <Image src={brandDark} alt={t("logoAlt")} priority className="hidden h-full w-auto dark:block" />
      <Image src={brandLight} alt={t("logoAlt")} priority className="block h-full w-auto dark:hidden" />
    </span>
  );
}

/** Hexagon icon only (keeps its gradient on both themes). */
export function LogoIcon({ className }: { className?: string }) {
  const t = useTranslations("common");
  return <Image src={icon} alt={t("logoAlt")} className={cn("h-full w-auto", className)} />;
}
