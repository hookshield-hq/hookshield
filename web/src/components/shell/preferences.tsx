"use client";

import { useRef, useState, useTransition } from "react";
import { useLocale, useTranslations } from "next-intl";
import { useTheme } from "next-themes";
import { Check, Languages, Monitor, Moon, Sun } from "lucide-react";
import { usePathname, useRouter } from "@/i18n/navigation";
import { locales, type Locale } from "@/i18n/routing";
import { useMe, useUpdateMe } from "@/lib/auth";
import { cn } from "@/lib/cn";
import { useDismiss } from "@/components/ui/use-dismiss";
import { useResetOnHide } from "@/lib/use-reset-on-hide";

export const themes = ["dark", "light", "system"] as const;
export type Theme = (typeof themes)[number];
export const themeIcons = { dark: Moon, light: Sun, system: Monitor };

/** Changes the UI language (URL) and saves it on the account (emails and alerts use it too). */
export function useSwitchLocale() {
  const router = useRouter();
  const pathname = usePathname();
  const me = useMe();
  const update = useUpdateMe();
  const [, start] = useTransition();
  return (next: Locale) => {
    if (me.data && me.data.locale !== next) update.mutate({ locale: next });
    start(() => router.replace(pathname, { locale: next }));
  };
}

/** Changes the theme and saves it on the account. */
export function useSwitchTheme() {
  const { setTheme } = useTheme();
  const me = useMe();
  const update = useUpdateMe();
  return (next: Theme) => {
    setTheme(next);
    if (me.data && me.data.theme_preference !== next) update.mutate({ theme_preference: next });
  };
}

function Menu({
  label,
  icon,
  children,
}: {
  label: string;
  icon: React.ReactNode;
  children: (close: () => void) => React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useDismiss(open, ref, () => setOpen(false));
  useResetOnHide(() => setOpen(false));
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="inline-flex size-9 items-center justify-center rounded-lg text-muted transition hover:bg-hover hover:text-fg"
      >
        {icon}
      </button>
      {open && (
        <div role="menu" className="panel absolute right-0 z-40 mt-2 min-w-48 rounded-xl p-1">
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  );
}

function MenuItem({ selected, onSelect, children }: { selected?: boolean; onSelect: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={selected}
      onClick={onSelect}
      className="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm text-fg transition hover:bg-hover"
    >
      <span className="flex-1">{children}</span>
      <Check className={cn("size-4 text-accent", !selected && "invisible")} />
    </button>
  );
}

export function LocaleSwitcher() {
  const t = useTranslations("shell.language");
  const locale = useLocale() as Locale;
  const switchLocale = useSwitchLocale();
  return (
    <Menu label={t("label")} icon={<Languages className="size-4" />}>
      {(close) =>
        locales.map((l) => (
          <MenuItem
            key={l}
            selected={l === locale}
            onSelect={() => {
              close();
              switchLocale(l);
            }}
          >
            <span lang={l}>{t(l)}</span>
          </MenuItem>
        ))
      }
    </Menu>
  );
}

export function ThemeSwitcher() {
  const t = useTranslations("shell.theme");
  const { theme, resolvedTheme } = useTheme();
  const switchTheme = useSwitchTheme();
  const Icon = resolvedTheme === "light" ? Sun : Moon;
  return (
    <Menu label={t("label")} icon={<Icon className="size-4" />}>
      {(close) =>
        themes.map((th) => {
          const ThIcon = themeIcons[th];
          return (
            <MenuItem
              key={th}
              selected={theme === th}
              onSelect={() => {
                close();
                switchTheme(th);
              }}
            >
              <span className="inline-flex items-center gap-2">
                <ThIcon className="size-4 text-muted" />
                {t(th)}
              </span>
            </MenuItem>
          );
        })
      }
    </Menu>
  );
}
