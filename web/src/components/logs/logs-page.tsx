"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { ArrowUp, Radio, Search, X } from "lucide-react";
import { LOG_STATUSES, useEndpoints, useLogs, type LogFilters, type LogStatus, type LogSummary } from "@/lib/queries";
import { useLiveEvents, type LiveEvent } from "@/lib/live";
import { formatDateTime, formatMs, formatRelative } from "@/lib/format";
import { useErrorMessage } from "@/lib/use-error-message";
import { Badge, logTone, statusTone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Select } from "@/components/ui/field";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { cn } from "@/lib/cn";
import { LogDebugger } from "./log-debugger";

type Range = "all" | "1h" | "24h" | "7d" | "30d";
const RANGE_MS: Record<Exclude<Range, "all">, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 604_800_000, "30d": 2_592_000_000 };

type Page = { logs: LogSummary[]; next_cursor: string | null };

/** Keeps ?id= (and the filters) in the address bar without a navigation. */
function syncURL(params: Record<string, string | undefined>) {
  const url = new URL(window.location.href);
  for (const [k, v] of Object.entries(params)) {
    if (v) url.searchParams.set(k, v);
    else url.searchParams.delete(k);
  }
  window.history.replaceState(window.history.state, "", url);
}

export function LogsPage() {
  const t = useTranslations("logs");
  const common = useTranslations("common");
  const locale = useLocale();
  const errorMessage = useErrorMessage();
  const qc = useQueryClient();
  const params = useSearchParams();
  const endpoints = useEndpoints();

  const [endpointId, setEndpointId] = useState(params.get("endpoint") ?? "");
  const [statuses, setStatuses] = useState<LogStatus[]>(() => (params.get("status") ?? "").split(",").filter((s): s is LogStatus => LOG_STATUSES.includes(s as LogStatus)));
  const [range, setRange] = useState<Range>("all");
  const [from, setFrom] = useState<string | undefined>();
  const [search, setSearch] = useState("");
  const [q, setQ] = useState("");
  const [selected, setSelected] = useState<string | null>(params.get("id"));
  const [fresh, setFresh] = useState(0);

  // Links to /logs?id=… (⌘K, other screens) while this page is already open.
  const linked = params.get("id");
  const [lastLinked, setLastLinked] = useState(linked);
  if (linked !== lastLinked) {
    setLastLinked(linked);
    if (linked) setSelected(linked);
  }

  // Debounce the search box.
  useEffect(() => {
    const id = setTimeout(() => setQ(search.trim()), 300);
    return () => clearTimeout(id);
  }, [search]);

  useEffect(() => {
    syncURL({ endpoint: endpointId || undefined, status: statuses.join(",") || undefined });
  }, [endpointId, statuses]);

  const filters: LogFilters = useMemo(() => ({ endpoint_id: endpointId || undefined, status: statuses, q: q || undefined, from }), [endpointId, statuses, q, from]);
  const logs = useLogs(filters);
  const rows = useMemo(() => logs.data?.pages.flatMap((p) => p.logs) ?? [], [logs.data]);

  const open = useCallback((id: string | null) => {
    setSelected(id);
    syncURL({ id: id ?? undefined });
  }, []);

  // Live feed: update rows in place, count new webhooks instead of moving the list under the cursor.
  const onEvent = useCallback(
    (e: LiveEvent) => {
      if (e.type === "log.created") {
        if (!endpointId || e.endpoint_id === endpointId) setFresh((n) => n + 1);
        return;
      }
      qc.setQueriesData<InfiniteData<Page>>({ queryKey: ["logs"] }, (old) =>
        old
          ? {
              ...old,
              pages: old.pages.map((p) => ({
                ...p,
                logs: p.logs.map((l) =>
                  l.id === e.log_id
                    ? { ...l, status: e.status as LogStatus, attempts: e.attempt ?? l.attempts, last_status_code: e.status_code ?? l.last_status_code }
                    : l,
                ),
              })),
            }
          : old,
      );
      void qc.invalidateQueries({ queryKey: ["log", e.log_id] });
    },
    [endpointId, qc],
  );
  const live = useLiveEvents(onEvent, { onReconnect: () => void logs.refetch() });

  const showFresh = () => {
    setFresh(0);
    if (range !== "all") setFrom(new Date(Date.now() - RANGE_MS[range]).toISOString());
    void logs.refetch();
  };

  const toggleStatus = (s: LogStatus) => setStatuses((cur) => (cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s]));
  const filtered = !!(endpointId || statuses.length || q || range !== "all");
  const clear = () => {
    setEndpointId("");
    setStatuses([]);
    setRange("all");
    setFrom(undefined);
    setSearch("");
  };

  return (
    <div className="mx-auto max-w-7xl">
      <PageHeader
        title={t("title")}
        description={t("subtitle")}
        actions={
          <span className="inline-flex items-center gap-2 text-xs text-muted" role="status">
            <span className={cn("size-2 rounded-full", live === "open" ? "animate-pulse bg-success" : "bg-subtle")} />
            {live === "open" ? t("live.on") : t("live.off")}
          </span>
        }
      />

      <Card className="mb-4 p-4">
        <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-subtle" />
            <input
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t("filters.search")}
              aria-label={t("filters.search")}
              className="h-10 w-full rounded-lg border border-line bg-surface-strong/60 pr-3 pl-9 text-sm text-fg outline-none transition placeholder:text-subtle focus:border-accent focus:ring-2 focus:ring-accent/25"
            />
          </div>
          <div className="grid grid-cols-2 gap-3 lg:w-96">
            <Select aria-label={t("filters.endpoint")} value={endpointId} onChange={(e) => setEndpointId(e.target.value)}>
              <option value="">{t("filters.allEndpoints")}</option>
              {(endpoints.data ?? []).map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                </option>
              ))}
            </Select>
            <Select
              aria-label={t("filters.period")}
              value={range}
              onChange={(e) => {
                const r = e.target.value as Range;
                setRange(r);
                setFrom(r === "all" ? undefined : new Date(Date.now() - RANGE_MS[r]).toISOString());
              }}
            >
              <option value="all">{t("filters.anyTime")}</option>
              <option value="1h">{t("filters.last1h")}</option>
              <option value="24h">{t("filters.last24h")}</option>
              <option value="7d">{t("filters.last7d")}</option>
              <option value="30d">{t("filters.last30d")}</option>
            </Select>
          </div>
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-1.5" role="group" aria-label={t("filters.status")}>
          {LOG_STATUSES.map((s) => {
            const on = statuses.includes(s);
            return (
              <button
                key={s}
                type="button"
                aria-pressed={on}
                onClick={() => toggleStatus(s)}
                className={cn("inline-flex h-7 items-center gap-1.5 rounded-full border px-3 text-xs transition", on ? "border-accent bg-hover text-fg" : "border-line text-muted hover:text-fg")}
              >
                <span className={cn("size-1.5 rounded-full", { success: "bg-success", warning: "bg-warning", danger: "bg-danger", info: "bg-info", neutral: "bg-subtle" }[logTone(s)])} />
                {t(`status.${s}`)}
              </button>
            );
          })}
          {filtered && (
            <Button variant="ghost" size="sm" onClick={clear} className="ml-auto">
              <X className="size-3.5" />
              {t("filters.clear")}
            </Button>
          )}
        </div>
      </Card>

      {fresh > 0 && (
        <div className="mb-3 flex justify-center">
          <Button variant="secondary" size="sm" onClick={showFresh}>
            <ArrowUp className="size-3.5" />
            {t("live.fresh", { n: fresh })}
          </Button>
        </div>
      )}

      <Card>
        {logs.isPending ? (
          <div className="flex flex-col gap-2 p-5" aria-busy="true">
            {[0, 1, 2, 3, 4].map((i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
            <span className="sr-only">{common("loading")}</span>
          </div>
        ) : logs.isError ? (
          <EmptyState title={common("errorTitle")} body={errorMessage(logs.error)} action={<Button onClick={() => logs.refetch()}>{common("retry")}</Button>} />
        ) : rows.length === 0 ? (
          filtered ? (
            <EmptyState icon={<Search className="size-6" />} title={t("emptyFiltered")} body={t("emptyFilteredBody")} action={<Button variant="secondary" onClick={clear}>{t("filters.clear")}</Button>} />
          ) : (
            <EmptyState icon={<Radio className="size-6" />} title={t("empty")} body={t("emptyBody")} />
          )
        ) : (
          <>
            <Table>
              <THead>
                <TR className="hover:bg-transparent">
                  <TH>{t("columns.status")}</TH>
                  <TH>{t("columns.event")}</TH>
                  <TH>{t("columns.endpoint")}</TH>
                  <TH>{t("columns.response")}</TH>
                  <TH className="text-right">{t("columns.latency")}</TH>
                  <TH className="text-right">{t("columns.received")}</TH>
                </TR>
              </THead>
              <TBody>
                {rows.map((l) => (
                  <TR
                    key={l.id}
                    tabIndex={0}
                    aria-selected={selected === l.id}
                    onClick={() => open(l.id)}
                    onKeyDown={(e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), open(l.id))}
                    className={cn("cursor-pointer outline-none focus-visible:bg-hover", selected === l.id && "bg-hover")}
                  >
                    <TD>
                      <Badge tone={logTone(l.status)}>{t(`status.${l.status}`)}</Badge>
                    </TD>
                    <TD className="max-w-72">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-xs text-subtle">{l.method}</span>
                        <span className="truncate text-fg">{l.event_type ?? t("noEventType")}</span>
                        {l.replay_of && <Badge tone="info">{t("replayBadge")}</Badge>}
                      </div>
                      <span className="block font-mono text-[11px] text-subtle">{l.id.slice(0, 13)}…</span>
                    </TD>
                    <TD className="max-w-48 truncate text-muted">{l.endpoint.name}</TD>
                    <TD>
                      <div className="flex items-center gap-2">
                        {l.last_status_code != null ? <Badge tone={statusTone(l.last_status_code)}>{l.last_status_code}</Badge> : <span className="text-subtle">—</span>}
                        {l.attempts > 1 && <span className="text-xs text-subtle">{t("attempts", { n: l.attempts })}</span>}
                      </div>
                    </TD>
                    <TD className="text-right text-xs text-muted tabular-nums">{l.latency.gateway_ms != null ? formatMs(locale, l.latency.gateway_ms) : "—"}</TD>
                    <TD className="text-right text-xs whitespace-nowrap text-muted" title={formatDateTime(locale, l.received_at)}>
                      {formatRelative(locale, l.received_at)}
                    </TD>
                  </TR>
                ))}
              </TBody>
            </Table>
            {logs.hasNextPage && (
              <div className="flex justify-center border-t border-line p-3">
                <Button variant="ghost" size="sm" loading={logs.isFetchingNextPage} onClick={() => logs.fetchNextPage()}>
                  {common("loadMore")}
                </Button>
              </div>
            )}
          </>
        )}
      </Card>
      <LogDebugger id={selected} onClose={() => open(null)} onOpen={open} />
    </div>
  );
}
