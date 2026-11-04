"use client";

import { useMemo, useState, type ReactNode } from "react";
import { useLocale, useTranslations } from "next-intl";
import { Activity, Gauge, RotateCcw, ShieldCheck } from "lucide-react";
import { useMe } from "@/lib/auth";
import { useEndpoints, useMetricsSeries, useMetricsSummary, type MetricsRange } from "@/lib/queries";
import { formatMs, formatNumber, formatPercent } from "@/lib/format";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { LiveFeed } from "./live-feed";
import { Onboarding } from "./onboarding";
import { TrafficChart, type ChartSeries } from "./traffic-chart";

function MetricCard({ icon, label, value, sub, loading }: { icon: ReactNode; label: string; value: string; sub?: string; loading: boolean }) {
  return (
    <Card className="p-5">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted">{label}</span>
        <span className="flex size-8 items-center justify-center rounded-lg bg-hover text-accent">{icon}</span>
      </div>
      {loading ? <Skeleton className="mt-3 h-8 w-24" /> : <div className="mt-2 text-2xl font-semibold tracking-tight text-fg tabular-nums">{value}</div>}
      {sub && !loading && <div className="mt-1 text-xs text-subtle">{sub}</div>}
    </Card>
  );
}

type View = "traffic" | "latency" | "errors";

export function Dashboard() {
  const t = useTranslations("dashboard");
  const locale = useLocale();
  const me = useMe();
  const endpoints = useEndpoints();
  const [range, setRange] = useState<MetricsRange>("24h");
  const [view, setView] = useState<View>("traffic");
  const summary = useMetricsSummary(range);
  const series = useMetricsSeries(range);
  const allTime = useMetricsSummary("30d");
  const s = summary.data;
  const name = me.data?.name?.trim();

  const chart = useMemo(() => {
    const pts = series.data?.points ?? [];
    const defs: Record<View, ChartSeries[]> = {
      traffic: [
        { key: "received", label: t("chart.received"), color: "--hs-accent-from", fill: true },
        { key: "delivered", label: t("chart.delivered"), color: "--hs-success" },
        { key: "failed", label: t("chart.failed"), color: "--hs-danger" },
      ],
      latency: [{ key: "avg_latency_ms", label: t("chart.latency"), color: "--hs-accent-to", fill: true }],
      errors: [{ key: "error_rate", label: t("chart.errorRate"), color: "--hs-danger", fill: true }],
    };
    return {
      series: defs[view],
      points: pts.map((p) => ({ at: p.at, values: { received: p.received, delivered: p.delivered, failed: p.failed, avg_latency_ms: p.received ? p.avg_latency_ms : null, error_rate: p.error_rate } })),
    };
  }, [series.data, view, t]);

  const formatY = useMemo(() => {
    if (view === "latency") return (v: number) => formatMs(locale, v);
    if (view === "errors") return (v: number) => formatPercent(locale, v, 1);
    return (v: number) => formatNumber(locale, v, { notation: "compact" });
  }, [view, locale]);
  const bucket = series.data?.bucket_seconds ?? 3600;
  const formatX = useMemo(() => {
    const opts: Intl.DateTimeFormatOptions = bucket >= 86_400 ? { day: "numeric", month: "short" } : bucket >= 21_600 ? { day: "numeric", hour: "2-digit" } : { hour: "2-digit", minute: "2-digit" };
    const f = new Intl.DateTimeFormat(locale, opts);
    return (iso: string) => f.format(new Date(iso));
  }, [bucket, locale]);
  const formatTip = useMemo(() => {
    const f = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" });
    return (iso: string) => f.format(new Date(iso));
  }, [locale]);

  const loading = summary.isPending;
  const noEndpoints = endpoints.data?.length === 0;
  const noTraffic = (allTime.data?.total_received ?? 0) === 0;

  return (
    <div className="mx-auto max-w-7xl">
      <PageHeader
        title={name ? t("welcome", { name }) : t("welcomeNoName")}
        description={t("subtitle")}
        actions={
          <Segmented
            size="sm"
            label={t("range")}
            value={range}
            onChange={setRange}
            options={[
              { value: "1h", label: t("ranges.1h") },
              { value: "24h", label: t("ranges.24h") },
              { value: "7d", label: t("ranges.7d") },
              { value: "30d", label: t("ranges.30d") },
            ]}
          />
        }
      />

      {endpoints.data && allTime.data && (noEndpoints || noTraffic) && <Onboarding hasEndpoint={!noEndpoints} hasWebhook={!noTraffic} firstEndpointId={endpoints.data[0]?.id} />}

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricCard icon={<Activity className="size-4" />} label={t("cards.received")} loading={loading} value={s ? formatNumber(locale, s.total_received) : "—"} sub={s ? t("cards.inFlight", { n: formatNumber(locale, s.in_flight) }) : undefined} />
        <MetricCard
          icon={<ShieldCheck className="size-4" />}
          label={t("cards.deliveryRate")}
          loading={loading}
          value={s?.delivery_rate != null ? formatPercent(locale, s.delivery_rate, 2) : "—"}
          sub={s ? t("cards.deliveredFailed", { delivered: formatNumber(locale, s.delivered), failed: formatNumber(locale, s.failed) }) : undefined}
        />
        <MetricCard icon={<Gauge className="size-4" />} label={t("cards.latency")} loading={loading} value={s ? formatMs(locale, s.avg_latency_ms) : "—"} sub={s ? t("cards.p95", { ms: formatMs(locale, s.p95_latency_ms) }) : undefined} />
        <MetricCard icon={<RotateCcw className="size-4" />} label={t("cards.retries")} loading={loading} value={s ? formatNumber(locale, s.active_retries) : "—"} sub={t("cards.retriesHint")} />
      </div>

      <Card className="mb-6">
        <CardHeader>
          <CardTitle>{t("chart.title")}</CardTitle>
          <Segmented
            size="sm"
            label={t("chart.view")}
            value={view}
            onChange={setView}
            options={[
              { value: "traffic", label: t("chart.traffic") },
              { value: "latency", label: t("chart.latency") },
              { value: "errors", label: t("chart.errorRate") },
            ]}
          />
        </CardHeader>
        <div className="px-3 pt-4 pb-3">
          {series.isPending ? (
            <Skeleton className="h-[260px] w-full" />
          ) : (
            <TrafficChart
              points={chart.points}
              series={chart.series}
              formatY={formatY}
              formatX={formatX}
              formatTooltipX={formatTip}
              integer={view === "traffic"}
              label={s ? t("chart.summary", { received: formatNumber(locale, s.total_received), rate: s.delivery_rate != null ? formatPercent(locale, s.delivery_rate) : "—" }) : t("chart.title")}
            />
          )}
          <div className="mt-2 flex flex-wrap gap-4 px-2 text-xs text-muted">
            {chart.series.map((x) => (
              <span key={x.key} className="inline-flex items-center gap-1.5">
                <span className="size-2 rounded-full" style={{ background: `var(${x.color})` }} />
                {x.label}
              </span>
            ))}
          </div>
        </div>
      </Card>

      <LiveFeed />
    </div>
  );
}
