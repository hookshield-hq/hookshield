// Durations in the retry policy editor: the API speaks milliseconds, people
// speak seconds, minutes and hours.

export type DurationUnit = "s" | "min" | "h" | "d";

export const UNIT_MS: Record<DurationUnit, number> = { s: 1_000, min: 60_000, h: 3_600_000, d: 86_400_000 };

/** The largest unit that divides ms exactly (10 000 → 10 s, 3 600 000 → 1 h). */
export function splitDuration(ms: number): { value: number; unit: DurationUnit } {
  for (const unit of ["d", "h", "min"] as DurationUnit[]) {
    if (ms >= UNIT_MS[unit] && ms % UNIT_MS[unit] === 0) return { value: ms / UNIT_MS[unit], unit };
  }
  return { value: Math.round(ms / 1000), unit: "s" };
}

/** Compact, locale-aware duration: "10 s", "2 min", "1 h 30 min", "2 d". */
export function formatDuration(locale: string, ms: number): string {
  const nf = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 });
  if (ms < 1000) return `${nf.format(ms)} ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${nf.format(s)} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}

export type RetryPolicy = {
  strategy: "fixed" | "exponential" | "custom";
  max_retries: number;
  initial_interval_ms: number;
  multiplier: number;
  max_delay_ms: number;
  custom_schedule_ms: number[];
};

/**
 * Delay before each retry, as the dispatcher computes it (docs/DELIVERY.md),
 * without the ±10 % jitter: fixed = initial; exponential = initial ×
 * multiplier^(n−1); custom = the list, the last delay repeating. Every delay is
 * capped by max_delay_ms and never shorter than 1 s; custom without a list
 * falls back to the initial interval.
 */
export function retrySchedule(p: RetryPolicy): number[] {
  const out: number[] = [];
  for (let n = 1; n <= p.max_retries; n++) {
    let d: number;
    if (p.strategy === "fixed") d = p.initial_interval_ms;
    else if (p.strategy === "exponential") d = p.initial_interval_ms * Math.pow(p.multiplier, n - 1);
    else {
      const list = p.custom_schedule_ms;
      d = list.length ? list[Math.min(n - 1, list.length - 1)] : p.initial_interval_ms;
    }
    out.push(Math.max(1000, Math.min(d, p.max_delay_ms)));
  }
  return out;
}
