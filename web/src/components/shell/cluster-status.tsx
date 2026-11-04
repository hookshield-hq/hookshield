"use client";

import { useQuery } from "@tanstack/react-query";
import { useLocale, useTranslations } from "next-intl";
import { api, data } from "@/lib/api/client";
import { formatNumber, formatPercent } from "@/lib/format";
import { cn } from "@/lib/cn";

/** Header badge: pulsing dot + last hour's throughput and success rate. */
export function ClusterStatus() {
  const t = useTranslations("shell.status");
  const locale = useLocale();
  const status = useQuery({
    queryKey: ["status"],
    queryFn: async () => data(await (await api()).GET("/v1/status")),
    refetchInterval: 30_000,
  });
  const summary = useQuery({
    queryKey: ["metrics", "summary", "1h"],
    queryFn: async () => data(await (await api()).GET("/v1/metrics/summary", { params: { query: { range: "1h" } } })),
    refetchInterval: 30_000,
  });

  const state = status.data?.status ?? (status.isError ? "unknown" : undefined);
  const tone = state === "operational" ? "bg-success" : state === "degraded" ? "bg-warning" : "bg-subtle";
  const s = summary.data;
  const detail =
    s && s.total_received > 0
      ? t("summary", {
          rate: formatNumber(locale, s.total_received / 60, { maximumFractionDigits: 1 }),
          success: s.delivery_rate == null ? "—" : formatPercent(locale, s.delivery_rate, 2),
        })
      : s
        ? t("noTraffic")
        : null;

  return (
    <div role="status" aria-label={t("label")} className="hidden items-center gap-2 text-xs text-muted lg:flex">
      <span className="relative flex size-2">
        {state === "operational" && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-60", tone)} />}
        <span className={cn("relative inline-flex size-2 rounded-full", tone)} />
      </span>
      <span className="font-medium text-fg">{state ? t(state as "operational" | "degraded" | "unknown") : null}</span>
      {detail && <span className="tabular-nums">{detail}</span>}
    </div>
  );
}
