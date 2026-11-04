"use client";

import { useState } from "react";
import { useLocale, useTranslations } from "next-intl";
import { ArrowLeft, Pause, Play, Send, Trash2, Webhook } from "lucide-react";
import { Link, useRouter } from "@/i18n/navigation";
import { useDeleteEndpoint, useEndpoint, useLogs, useMetricsSummary, useTestEndpoint, useUpdateEndpoint, type Endpoint } from "@/lib/queries";
import { formatMs, formatNumber, formatPercent, formatRelative } from "@/lib/format";
import { useErrorMessage } from "@/lib/use-error-message";
import { Badge, logTone, statusTone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code";
import { ConfirmDialog } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { useToast } from "@/components/ui/toast";
import { AlertsSection, DeliverySection, GeneralSection, SecuritySection, TransformSummary } from "./endpoint-sections";
import { CopyField, curlExample, EndpointStatusBadge } from "./shared";

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-line bg-surface-strong/40 p-3">
      <div className="text-xs text-subtle">{label}</div>
      <div className="mt-1 text-lg font-semibold text-fg tabular-nums">{value}</div>
    </div>
  );
}

function Overview({ endpoint }: { endpoint: Endpoint }) {
  const t = useTranslations();
  const locale = useLocale();
  const router = useRouter();
  const summary = useMetricsSummary("24h", endpoint.id);
  const logs = useLogs({ endpoint_id: endpoint.id }, 5);
  const recent = logs.data?.pages[0]?.logs ?? [];
  const s = summary.data;
  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-5">
      <Card className="lg:col-span-3">
        <CardHeader>
          <div>
            <CardTitle>{t("endpoints.detail.receive")}</CardTitle>
            <p className="mt-0.5 text-xs text-muted">{t("endpoints.detail.receiveHint")}</p>
          </div>
        </CardHeader>
        <CardBody className="flex flex-col gap-4">
          <CopyField value={endpoint.ingest_url} label={t("endpoints.copyUrl")} />
          <CodeBlock value={curlExample(endpoint)} />
          <p className="text-xs text-subtle">{t("endpoints.detail.forwardsTo", { url: endpoint.target_url })}</p>
        </CardBody>
      </Card>
      <Card className="lg:col-span-2">
        <CardHeader>
          <CardTitle>{t("endpoints.detail.last24h")}</CardTitle>
        </CardHeader>
        <CardBody className="grid grid-cols-2 gap-3">
          <Stat label={t("dashboard.cards.received")} value={s ? formatNumber(locale, s.total_received) : "—"} />
          <Stat label={t("dashboard.cards.deliveryRate")} value={s?.delivery_rate != null ? formatPercent(locale, s.delivery_rate) : "—"} />
          <Stat label={t("dashboard.cards.latency")} value={s ? formatMs(locale, s.avg_latency_ms) : "—"} />
          <Stat label={t("dashboard.cards.retries")} value={s ? formatNumber(locale, s.active_retries) : "—"} />
        </CardBody>
      </Card>
      <Card className="lg:col-span-5">
        <CardHeader>
          <CardTitle>{t("endpoints.detail.recent")}</CardTitle>
          <Link href={`/logs?endpoint=${endpoint.id}`} className="text-xs font-medium text-accent hover:underline">
            {t("endpoints.detail.allLogs")}
          </Link>
        </CardHeader>
        {logs.isPending ? (
          <CardBody>
            <Skeleton className="h-24 w-full" />
          </CardBody>
        ) : recent.length === 0 ? (
          <EmptyState icon={<Webhook className="size-6" />} title={t("endpoints.detail.noLogs")} body={t("endpoints.detail.noLogsBody")} />
        ) : (
          <ul className="divide-y divide-line">
            {recent.map((l) => (
              <li key={l.id}>
                <button type="button" onClick={() => router.push(`/logs?id=${l.id}`)} className="flex w-full items-center gap-3 px-5 py-3 text-left text-sm transition hover:bg-hover/60">
                  <Badge tone={logTone(l.status)}>{t(`logs.status.${l.status}`)}</Badge>
                  <span className="font-mono text-xs text-muted">{l.method}</span>
                  <span className="min-w-0 flex-1 truncate text-fg">{l.event_type ?? t("logs.noEventType")}</span>
                  {l.last_status_code != null && <Badge tone={statusTone(l.last_status_code)}>{l.last_status_code}</Badge>}
                  <span className="text-xs whitespace-nowrap text-subtle">{formatRelative(locale, l.received_at)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}

export function EndpointDetail({ id }: { id: string }) {
  const t = useTranslations("endpoints");
  const common = useTranslations("common");
  const router = useRouter();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const q = useEndpoint(id);
  const update = useUpdateEndpoint(id);
  const del = useDeleteEndpoint();
  const test = useTestEndpoint();
  const [tab, setTab] = useState("overview");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const back = (
    <Link href="/endpoints" className="inline-flex items-center gap-1 text-xs font-medium text-muted transition hover:text-fg">
      <ArrowLeft className="size-3.5" />
      {t("title")}
    </Link>
  );

  if (q.isPending)
    return (
      <div className="mx-auto max-w-6xl" aria-busy="true">
        <Skeleton className="mb-6 h-10 w-72" />
        <Skeleton className="h-64 w-full" />
      </div>
    );
  if (q.isError)
    return (
      <div className="mx-auto max-w-6xl">
        <PageHeader title={t("title")} back={back} />
        <Card>
          <EmptyState title={common("errorTitle")} body={errorMessage(q.error)} action={<Button onClick={() => router.push("/endpoints")}>{common("back")}</Button>} />
        </Card>
      </div>
    );

  const e = q.data.endpoint;
  const paused = e.status === "paused";

  return (
    <div className="mx-auto max-w-6xl">
      <PageHeader
        back={back}
        title={
          <span className="flex items-center gap-3">
            <span className="truncate">{e.name}</span>
            <EndpointStatusBadge status={e.status} />
          </span>
        }
        description={e.description || undefined}
        actions={
          <>
            <Button
              variant="secondary"
              disabled={paused}
              loading={test.isPending}
              onClick={() =>
                test.mutate(
                  { id },
                  {
                    onSuccess: (r) => {
                      toast.success(t("create.testSent"));
                      router.push(`/logs?id=${r.id}`);
                    },
                    onError: (err) => toast.error(errorMessage(err)),
                  },
                )
              }
            >
              <Send className="size-4" />
              {t("detail.sendTest")}
            </Button>
            <Button
              variant="secondary"
              loading={update.isPending}
              onClick={() =>
                update.mutate(
                  { status: paused ? "active" : "paused" },
                  { onSuccess: () => toast.success(paused ? t("detail.resumed") : t("detail.pausedToast")), onError: (err) => toast.error(errorMessage(err)) },
                )
              }
            >
              {paused ? <Play className="size-4" /> : <Pause className="size-4" />}
              {paused ? t("detail.resume") : t("detail.pause")}
            </Button>
            <Button variant="ghost" size="icon" aria-label={t("detail.delete")} title={t("detail.delete")} onClick={() => setConfirmDelete(true)}>
              <Trash2 className="size-4" />
            </Button>
          </>
        }
      />
      <Tabs
        label={t("detail.sections")}
        value={tab}
        onChange={setTab}
        tabs={[
          { id: "overview", label: t("detail.tabs.overview"), content: <Overview endpoint={e} /> },
          { id: "settings", label: t("detail.tabs.settings"), content: <GeneralSection key={e.updated_at} endpoint={e} /> },
          { id: "security", label: t("detail.tabs.security"), content: <SecuritySection key={e.updated_at} endpoint={e} /> },
          { id: "delivery", label: t("detail.tabs.delivery"), content: <DeliverySection key={e.updated_at} endpoint={e} warnings={q.data.warnings ?? []} /> },
          { id: "transform", label: t("detail.tabs.transform"), content: <TransformSummary endpoint={e} /> },
          { id: "alerts", label: t("detail.tabs.alerts"), content: <AlertsSection key={e.updated_at} endpoint={e} /> },
        ]}
      />
      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title={t("detail.deleteTitle", { name: e.name })}
        body={t("detail.deleteBody")}
        confirmLabel={t("detail.delete")}
        loading={del.isPending}
        onConfirm={() =>
          del.mutate(id, {
            onSuccess: () => {
              toast.success(t("detail.deleted"));
              router.push("/endpoints");
            },
            onError: (err) => {
              setConfirmDelete(false);
              toast.error(errorMessage(err));
            },
          })
        }
      />
    </div>
  );
}
