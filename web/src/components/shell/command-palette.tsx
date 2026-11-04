"use client";

import { useEffect, useState } from "react";
import { Command } from "cmdk";
import { useQuery } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { Activity, Languages, Lock, Search, Webhook } from "lucide-react";
import { useRouter } from "@/i18n/navigation";
import { locales } from "@/i18n/routing";
import { api, data } from "@/lib/api/client";
import { useFeatures } from "@/lib/queries";
import { visibleNav } from "./nav";
import { themeIcons, themes, useSwitchLocale, useSwitchTheme } from "./preferences";

/** Opens with ⌘K / Ctrl+K anywhere; navigation, preferences and live search (design §1). */
export function useCommandPaletteShortcut(setOpen: (fn: (o: boolean) => boolean) => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((o) => !o);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [setOpen]);
}

function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

const itemClass =
  "flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-sm text-fg data-[selected=true]:bg-hover aria-disabled:opacity-50";
const groupClass =
  "px-1 py-1 [&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-xs [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-subtle";

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const t = useTranslations("shell.palette");
  const nav = useTranslations("nav");
  const tTheme = useTranslations("shell.theme");
  const tLang = useTranslations("shell.language");
  const router = useRouter();
  const switchLocale = useSwitchLocale();
  const features = useFeatures();
  const switchTheme = useSwitchTheme();
  const [query, setQuery] = useState("");
  const q = useDebounced(query.trim(), 200);

  const search = useQuery({
    queryKey: ["search", q],
    queryFn: async () => data(await (await api()).GET("/v1/search", { params: { query: { q } } })),
    enabled: open && q.length >= 2,
    staleTime: 10_000,
  });

  function run(fn: () => void) {
    onOpenChange(false);
    setQuery("");
    fn();
  }

  return (
    <Command.Dialog
      open={open}
      onOpenChange={onOpenChange}
      label={t("title")}
      shouldFilter={q.length < 2}
      overlayClassName="fixed inset-0 z-50 bg-overlay backdrop-blur-sm"
      contentClassName="panel fixed left-1/2 top-[15vh] z-50 w-[min(640px,calc(100vw-2rem))] -translate-x-1/2 overflow-hidden rounded-2xl"
    >
      <div className="flex items-center gap-3 border-b border-line px-4">
        <Search className="size-4 text-subtle" />
        <Command.Input
          value={query}
          onValueChange={setQuery}
          placeholder={t("placeholder")}
          className="h-12 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-subtle"
        />
      </div>
      <Command.List className="max-h-[60vh] overflow-y-auto p-1">
        <Command.Empty className="px-4 py-8 text-center text-sm text-muted">
          {search.isFetching ? t("searching") : t("empty")}
        </Command.Empty>

        {q.length >= 2 && search.data && (
          <>
            {search.data.endpoints.length > 0 && (
              <Command.Group heading={t("endpoints")} className={groupClass}>
                {search.data.endpoints.map((e) => (
                  <Command.Item key={e.id} value={`endpoint ${e.id}`} className={itemClass} onSelect={() => run(() => router.push(`/endpoints/${e.id}`))}>
                    <Webhook className="size-4 text-subtle" />
                    <span className="flex-1 truncate">{e.name}</span>
                    <span className="font-mono text-xs text-subtle">{e.slug}</span>
                  </Command.Item>
                ))}
              </Command.Group>
            )}
            {search.data.logs.length > 0 && (
              <Command.Group heading={t("logs")} className={groupClass}>
                {search.data.logs.map((l) => (
                  <Command.Item key={l.id} value={`log ${l.id}`} className={itemClass} onSelect={() => run(() => router.push(`/logs?id=${l.id}`))}>
                    <Activity className="size-4 text-subtle" />
                    <span className="flex-1 truncate font-mono text-xs">{l.id}</span>
                    <span className="truncate text-xs text-subtle">{l.event_type}</span>
                  </Command.Item>
                ))}
              </Command.Group>
            )}
            {search.data.secrets.length > 0 && (
              <Command.Group heading={t("secrets")} className={groupClass}>
                {search.data.secrets.map((s) => (
                  <Command.Item key={s.name} value={`secret ${s.name}`} className={itemClass} onSelect={() => run(() => router.push("/secrets"))}>
                    <Lock className="size-4 text-subtle" />
                    <span className="font-mono text-xs">{s.name}</span>
                  </Command.Item>
                ))}
              </Command.Group>
            )}
          </>
        )}

        {q.length < 2 && (
          <>
            <Command.Group heading={t("navigation")} className={groupClass}>
              {visibleNav(features.data).map((item) => {
                const Icon = item.icon;
                return (
                  <Command.Item key={item.href} value={nav(item.key)} className={itemClass} onSelect={() => run(() => router.push(item.href))}>
                    <Icon className="size-4 text-subtle" />
                    {nav(item.key)}
                  </Command.Item>
                );
              })}
            </Command.Group>
            <Command.Group heading={t("preferences")} className={groupClass}>
              {themes.map((th) => {
                const Icon = themeIcons[th];
                const label = t("switchTheme", { theme: tTheme(`inline.${th}`) });
                return (
                  <Command.Item key={th} value={label} className={itemClass} onSelect={() => run(() => switchTheme(th))}>
                    <Icon className="size-4 text-subtle" />
                    {label}
                  </Command.Item>
                );
              })}
              {locales.map((l) => {
                const label = t("switchLanguage", { language: tLang(l) });
                return (
                  <Command.Item key={l} value={label} className={itemClass} onSelect={() => run(() => switchLocale(l))}>
                    <Languages className="size-4 text-subtle" />
                    {label}
                  </Command.Item>
                );
              })}
            </Command.Group>
          </>
        )}
      </Command.List>
    </Command.Dialog>
  );
}
