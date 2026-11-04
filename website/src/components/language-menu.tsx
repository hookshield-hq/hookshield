import { Check, Languages } from "lucide-react";
import { localeNames, localeRoot, locales, type Locale } from "@/i18n";

/** Language picker: links to each landing page (a disclosure, no JavaScript). */
export function LanguageMenu({ locale, label }: { locale: Locale; label: string }) {
  return (
    <details className="relative">
      <summary
        aria-label={`${label}: ${localeNames[locale]}`}
        title={label}
        className="inline-flex h-9 cursor-pointer list-none items-center gap-1.5 rounded-lg px-2 text-sm text-muted hover:bg-hover hover:text-fg [&::-webkit-details-marker]:hidden"
      >
        <Languages className="size-4" aria-hidden />
        <span className="hidden lg:inline">{localeNames[locale]}</span>
      </summary>
      <ul className="panel absolute right-0 mt-2 w-44 rounded-xl p-1.5 text-sm">
        {locales.map((l) => (
          <li key={l}>
            <a
              href={localeRoot(l)}
              hrefLang={l}
              lang={l}
              aria-current={l === locale ? "true" : undefined}
              className="flex items-center justify-between rounded-lg px-3 py-2 text-muted hover:bg-hover hover:text-fg"
            >
              {localeNames[l]}
              {l === locale && <Check className="size-4 text-accent" aria-hidden />}
            </a>
          </li>
        ))}
      </ul>
    </details>
  );
}
