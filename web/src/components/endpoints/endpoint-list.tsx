"use client";

import { useMemo, useState } from "react";
import { useLocale, useTranslations } from "next-intl";
import { ChevronRight, Plus, Webhook } from "lucide-react";
import { Link, useRouter } from "@/i18n/navigation";
import { useEndpointMetrics, useEndpoints } from "@/lib/queries";
import { formatPercent, formatRelative, formatNumber } from "@/lib/format";
import { useErrorMessage } from "@/lib/use-error-message";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { CreateEndpoint } from "./create-endpoint";
import { EndpointStatusBadge } from "./shared";

/** Success-rate bar: green share of finished webhooks. */
function RateBar({ rate }: { rate: number | null }) {
  const locale = useLocale();
  if (rate == null) return <span className="text-subtle">—</span>;
  const tone = rate >= 99 ? "bg-success" : rate >= 90 ? "bg-warning" : "bg-danger";
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-16 overflow-hidden rounded-full bg-line">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.max(2, rate)}%` }} />
      </div>
      <span className="text-xs tabular-nums text-muted">{formatPercent(locale, rate)}</span>
    </div>
  );
}

export function EndpointList() {
  const t = useTranslations("endpoints");
  const common = useTranslations("common");
  const locale = useLocale();
  const router = useRouter();
  const errorMessage = useErrorMessage();
  const endpoints = useEndpoints();
  const metrics = useEndpointMetrics("24h");
  const [creating, setCreating] = useState(false);
  const byId = useMemo(() => new Map((metrics.data?.endpoints ?? []).map((m) => [m.endpoint_id, m])), [metrics.data]);

  const newButton = (
    <Button onClick={() => setCreating(true)}>
      <Plus className="size-4" />
      {t("new")}
    </Button>
  );

  return (
    <div className="mx-auto max-w-6xl">
      <PageHeader title={t("title")} description={t("subtitle")} actions={endpoints.data?.length ? newButton : undefined} />
      <Card>
        {endpoints.isPending ? (
          <div className="flex flex-col gap-3 p-5" aria-busy="true">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
            <span className="sr-only">{common("loading")}</span>
          </div>
        ) : endpoints.isError ? (
          <EmptyState title={common("errorTitle")} body={errorMessage(endpoints.error)} action={<Button onClick={() => endpoints.refetch()}>{common("retry")}</Button>} />
        ) : endpoints.data.length === 0 ? (
          <EmptyState icon={<Webhook className="size-6" />} title={t("emptyTitle")} body={t("emptyBody")} action={newButton} />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <TH>{t("columns.name")}</TH>
                <TH>{t("columns.url")}</TH>
                <TH>{t("columns.status")}</TH>
                <TH>{t("columns.traffic")}</TH>
                <TH>{t("columns.success")}</TH>
                <TH>{t("columns.last")}</TH>
                <TH>
                  <span className="sr-only">{common("open")}</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {endpoints.data.map((e) => {
                const m = byId.get(e.id);
                return (
                  <TR key={e.id} className="cursor-pointer" onClick={() => router.push(`/endpoints/${e.id}`)}>
                    <TD className="max-w-56">
                      <Link href={`/endpoints/${e.id}`} onClick={(ev) => ev.stopPropagation()} className="block truncate font-medium text-fg hover:text-accent">
                        {e.name}
                      </Link>
                      <span className="block truncate text-xs text-subtle">{e.description || e.target_url}</span>
                    </TD>
                    <TD className="max-w-72" onClick={(ev) => ev.stopPropagation()}>
                      <div className="flex items-center gap-1">
                        <code className="truncate font-mono text-xs text-muted" title={e.ingest_url}>
                          {e.ingest_url}
                        </code>
                        <CopyButton value={e.ingest_url} label={t("copyUrl")} />
                      </div>
                    </TD>
                    <TD>
                      <EndpointStatusBadge status={e.status} />
                    </TD>
                    <TD className="tabular-nums text-muted">{m ? formatNumber(locale, m.received) : "—"}</TD>
                    <TD>
                      <RateBar rate={m?.success_rate ?? null} />
                    </TD>
                    <TD className="text-xs whitespace-nowrap text-muted">{m?.last_received_at ? formatRelative(locale, m.last_received_at) : t("never")}</TD>
                    <TD className="w-8 text-subtle">
                      <ChevronRight className="size-4" />
                    </TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        )}
      </Card>
      <CreateEndpoint open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}
