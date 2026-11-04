"use client";

import { useId } from "react";
import { useLocale, useTranslations } from "next-intl";
import { Plus, Trash2 } from "lucide-react";
import { formatDuration, retrySchedule, splitDuration, UNIT_MS, type DurationUnit, type RetryPolicy } from "@/lib/duration";
import { Button } from "@/components/ui/button";
import { ChoiceCards } from "@/components/ui/segmented";
import { cn } from "@/lib/cn";

const UNITS: DurationUnit[] = ["s", "min", "h", "d"];

/** Number + unit (s, min, h, d) bound to a value in milliseconds. */
export function DurationInput({ label, ms, onChange, error, hint }: { label: string; ms: number; onChange: (ms: number) => void; error?: string; hint?: string }) {
  const t = useTranslations("endpoints.retry.units");
  const id = useId();
  const { value, unit } = splitDuration(ms);
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-muted">
        {label}
      </label>
      <div className={cn("flex h-10 overflow-hidden rounded-lg border border-line bg-surface-strong/60 focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25", error && "border-danger")}>
        <input
          id={id}
          type="number"
          min={1}
          inputMode="numeric"
          value={value}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange(Math.max(0, Math.round(Number(e.target.value) || 0)) * UNIT_MS[unit])}
          className="w-full min-w-0 bg-transparent px-3 text-sm text-fg tabular-nums outline-none"
        />
        <select
          aria-label={t("label")}
          value={unit}
          onChange={(e) => onChange(value * UNIT_MS[e.target.value as DurationUnit])}
          className="border-l border-line bg-transparent px-2 text-sm text-muted outline-none"
        >
          {UNITS.map((u) => (
            <option key={u} value={u}>
              {t(u)}
            </option>
          ))}
        </select>
      </div>
      {error ? (
        <p role="alert" className="text-xs text-danger">
          {error}
        </p>
      ) : hint ? (
        <p className="text-xs text-subtle">{hint}</p>
      ) : null}
    </div>
  );
}

function NumberInput({ label, value, onChange, min, max, step, suffix, error }: { label: string; value: number; onChange: (n: number) => void; min: number; max: number; step?: number; suffix?: string; error?: string }) {
  const id = useId();
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-xs font-medium text-muted">
        {label}
      </label>
      <div className={cn("flex h-10 items-center rounded-lg border border-line bg-surface-strong/60 pr-3 focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25", error && "border-danger")}>
        <input
          id={id}
          type="number"
          min={min}
          max={max}
          step={step}
          value={value}
          aria-invalid={error ? true : undefined}
          onChange={(e) => onChange(Number(e.target.value))}
          className="w-full min-w-0 bg-transparent px-3 text-sm text-fg tabular-nums outline-none"
        />
        {suffix && <span className="text-sm text-subtle">{suffix}</span>}
      </div>
      {error && (
        <p role="alert" className="text-xs text-danger">
          {error}
        </p>
      )}
    </div>
  );
}

/** Retry policy: strategy selector, parameters and a visual backoff timeline (design §3.5). */
export function RetryEditor({ value, onChange, fieldError }: { value: RetryPolicy; onChange: (p: RetryPolicy) => void; fieldError: (f: string) => string | undefined }) {
  const t = useTranslations("endpoints.retry");
  const locale = useLocale();
  const set = (patch: Partial<RetryPolicy>) => onChange({ ...value, ...patch });
  const schedule = retrySchedule(value);
  const longest = Math.max(1, ...schedule);
  const elapsed = schedule.reduce<number[]>((acc, ms) => [...acc, (acc.at(-1) ?? 0) + ms], []);

  return (
    <div className="flex flex-col gap-5">
      <ChoiceCards
        label={t("strategy")}
        value={value.strategy}
        onChange={(strategy) => set({ strategy, custom_schedule_ms: strategy === "custom" && value.custom_schedule_ms.length === 0 ? retrySchedule({ ...value, strategy: "exponential" }).slice(0, 5) : value.custom_schedule_ms })}
        options={[
          { value: "fixed", label: t("fixed"), description: t("fixedHint") },
          { value: "exponential", label: t("exponential"), description: t("exponentialHint") },
          { value: "custom", label: t("custom"), description: t("customHint") },
        ]}
      />
      {fieldError("retry") && (
        <p role="alert" className="text-xs text-danger">
          {fieldError("retry")}
        </p>
      )}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <NumberInput label={t("maxRetries")} value={value.max_retries} min={0} max={100} onChange={(n) => set({ max_retries: Math.round(n) })} error={fieldError("max_retries")} />
        {value.strategy !== "custom" && (
          <DurationInput label={t("initial")} ms={value.initial_interval_ms} onChange={(ms) => set({ initial_interval_ms: ms })} error={fieldError("initial_interval")} />
        )}
        {value.strategy === "exponential" && (
          <NumberInput label={t("multiplier")} value={value.multiplier} min={1} max={10} step={0.5} suffix="×" onChange={(n) => set({ multiplier: n })} error={fieldError("multiplier")} />
        )}
        <DurationInput label={t("maxDelay")} ms={value.max_delay_ms} onChange={(ms) => set({ max_delay_ms: ms })} error={fieldError("max_delay")} hint={t("maxDelayHint")} />
      </div>

      {value.strategy === "custom" && (
        <div className="flex flex-col gap-2">
          <span className="text-xs font-medium text-muted">{t("customSchedule")}</span>
          {value.custom_schedule_ms.map((ms, i) => (
            <div key={i} className="grid grid-cols-[2.5rem_minmax(0,1fr)_auto] items-end gap-2">
              <span className="pb-2.5 text-xs text-subtle tabular-nums">#{i + 1}</span>
              <DurationInput label={t("retryN", { n: i + 1 })} ms={ms} onChange={(v) => set({ custom_schedule_ms: value.custom_schedule_ms.map((x, j) => (j === i ? v : x)) })} />
              <Button variant="ghost" size="icon" aria-label={t("removeDelay")} onClick={() => set({ custom_schedule_ms: value.custom_schedule_ms.filter((_, j) => j !== i) })}>
                <Trash2 className="size-4" />
              </Button>
            </div>
          ))}
          {fieldError("custom_schedule") && (
            <p role="alert" className="text-xs text-danger">
              {fieldError("custom_schedule")}
            </p>
          )}
          {value.custom_schedule_ms.length < 100 && (
            <div>
              <Button variant="secondary" size="sm" onClick={() => set({ custom_schedule_ms: [...value.custom_schedule_ms, value.custom_schedule_ms.at(-1) ?? 60_000] })}>
                <Plus className="size-3.5" />
                {t("addDelay")}
              </Button>
            </div>
          )}
          <p className="text-xs text-subtle">{t("customRepeat")}</p>
        </div>
      )}

      <div className="rounded-lg border border-line bg-surface-strong/50 p-4">
        <div className="mb-3 flex items-baseline justify-between gap-4">
          <h4 className="text-xs font-semibold tracking-wide text-subtle uppercase">{t("preview")}</h4>
          {schedule.length > 0 && <span className="text-xs text-muted">{t("total", { time: formatDuration(locale, schedule.reduce((a, b) => a + b, 0)) })}</span>}
        </div>
        {schedule.length === 0 ? (
          <p className="text-xs text-muted">{t("noRetries")}</p>
        ) : (
          <ol className="flex flex-col gap-1.5">
            {schedule.slice(0, 12).map((ms, i) => {
              return (
                <li key={i} className="grid grid-cols-[4.5rem_minmax(0,1fr)_7rem] items-center gap-3 text-xs">
                  <span className="text-muted">{t("retryN", { n: i + 1 })}</span>
                  <div className="h-2 overflow-hidden rounded-full bg-line">
                    <div className="bg-accent-gradient h-full rounded-full" style={{ width: `${Math.max(3, (ms / longest) * 100)}%` }} />
                  </div>
                  <span className="text-right text-fg tabular-nums" title={t("after", { time: formatDuration(locale, elapsed[i]) })}>
                    +{formatDuration(locale, ms)}
                  </span>
                </li>
              );
            })}
            {schedule.length > 12 && <li className="text-xs text-subtle">{t("more", { n: schedule.length - 12 })}</li>}
          </ol>
        )}
        <p className="mt-3 text-xs text-subtle">{t("jitter")}</p>
      </div>
    </div>
  );
}
