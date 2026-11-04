// Locale-aware formatting with Intl (dates, numbers, relative time).
// Always pass the active locale from next-intl's useLocale().

export function formatNumber(locale: string, n: number, opts?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(locale, opts).format(n);
}

export function formatPercent(locale: string, pct: number, digits = 1): string {
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: digits }).format(pct / 100);
}

export function formatDateTime(locale: string, iso: string | Date): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(iso));
}

const units: Array<[Intl.RelativeTimeFormatUnit, number]> = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
  ["second", 1],
];

/** "2 minutes ago" / "há 2 minutos" / "hace 2 minutos". */
export function formatRelative(locale: string, iso: string | Date, now: Date = new Date()): string {
  const diff = (new Date(iso).getTime() - now.getTime()) / 1000;
  const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
  for (const [unit, secs] of units) {
    if (Math.abs(diff) >= secs || unit === "second") {
      return rtf.format(Math.round(diff / secs), unit);
    }
  }
  return rtf.format(0, "second");
}

/** Milliseconds with sensible precision: 0.42 ms, 12.3 ms, 1,204 ms. */
export function formatMs(locale: string, ms: number): string {
  const digits = ms < 1 ? 2 : ms < 100 ? 1 : 0;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(ms)} ms`;
}
