"use client";

import { useCallback, useMemo, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { useLocale, useTranslations } from "next-intl";
import { Radio } from "lucide-react";
import { Link, useRouter } from "@/i18n/navigation";
import { useEndpoints, useLogs } from "@/lib/queries";
import { useLiveEvents, type LiveEvent } from "@/lib/live";
import { formatMs, formatRelative } from "@/lib/format";
import { Badge, logTone, statusTone } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/cn";

type Row = { id: string; endpointId: string; status: string; method: string; eventType: string | null; latency: number | null; code: number | null; at: string; fresh?: boolean };

const MAX = 12;

/** Live Stream Feed (design §3.2): the latest webhooks, updated in real time over SSE. */
export function LiveFeed() {
  const t = useTranslations();
  const locale = useLocale();
  const router = useRouter();
  const endpoints = useEndpoints();
  const initial = useLogs({}, MAX);
  const [live, setLive] = useState<Row[]>([]);
  const names = useMemo(() => new Map((endpoints.data ?? []).map((e) => [e.id, e.name])), [endpoints.data]);

  const rows = useMemo(() => {
    const base: Row[] = (initial.data?.pages[0]?.logs ?? []).map((l) => ({
      id: l.id,
      endpointId: l.endpoint.id,
      status: l.status,
      method: l.method,
      eventType: l.event_type,
      latency: l.latency.gateway_ms,
      code: l.last_status_code,
      at: l.received_at,
    }));
    const seen = new Set(live.map((r) => r.id));
    return [...live, ...base.filter((r) => !seen.has(r.id))].slice(0, MAX);
  }, [initial.data, live]);

  const onEvent = useCallback(
    (e: LiveEvent) =>
      setLive((cur) => {
        if (e.type === "log.created") {
          const row: Row = { id: e.log_id, endpointId: e.endpoint_id, status: e.status, method: e.method ?? "POST", eventType: e.event_type ?? null, latency: e.latency_ms ?? null, code: null, at: e.at, fresh: true };
          return [row, ...cur.filter((r) => r.id !== e.log_id)].slice(0, MAX);
        }
        const known = cur.some((r) => r.id === e.log_id);
        if (known) return cur.map((r) => (r.id === e.log_id ? { ...r, status: e.status, code: e.status_code ?? r.code } : r));
        const fromList = initial.data?.pages[0]?.logs.find((l) => l.id === e.log_id);
        if (!fromList) return cur;
        return [
          { id: fromList.id, endpointId: fromList.endpoint.id, status: e.status, method: fromList.method, eventType: fromList.event_type, latency: fromList.latency.gateway_ms, code: e.status_code ?? fromList.last_status_code, at: fromList.received_at },
          ...cur,
        ].slice(0, MAX);
      }),
    [initial.data],
  );
  const state = useLiveEvents(onEvent, { onReconnect: () => void initial.refetch() });

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center gap-2">
          <CardTitle>{t("dashboard.feed.title")}</CardTitle>
          <span className={cn("size-2 rounded-full", state === "open" ? "animate-pulse bg-success" : "bg-subtle")} aria-hidden />
          <span className="sr-only">{state === "open" ? t("logs.live.on") : t("logs.live.off")}</span>
        </div>
        <Link href="/logs" className="text-xs font-medium text-accent hover:underline">
          {t("common.viewAll")}
        </Link>
      </CardHeader>
      {initial.isPending ? (
        <div className="flex flex-col gap-2 p-5">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      ) : rows.length === 0 ? (
        <EmptyState icon={<Radio className="size-6" />} title={t("dashboard.feed.empty")} body={t("dashboard.feed.emptyBody")} />
      ) : (
        <ul className="divide-y divide-line" aria-live="polite">
          <AnimatePresence initial={false}>
            {rows.map((r) => (
              <motion.li key={r.id} layout initial={r.fresh ? { opacity: 0, y: -8, backgroundColor: "var(--hs-hover)" } : false} animate={{ opacity: 1, y: 0, backgroundColor: "rgba(0,0,0,0)" }} transition={{ duration: 0.15, backgroundColor: { duration: 1.2 } }}>
                <button type="button" onClick={() => router.push(`/logs?id=${r.id}`)} className="grid w-full grid-cols-[6.5rem_3.5rem_minmax(0,1fr)_auto] items-center gap-3 px-5 py-2.5 text-left text-sm transition hover:bg-hover/60 sm:grid-cols-[6.5rem_3.5rem_minmax(0,1fr)_minmax(0,10rem)_4rem_5rem]">
                  <Badge tone={logTone(r.status)} className="justify-self-start">
                    {t(`logs.status.${r.status}`)}
                  </Badge>
                  <span className="font-mono text-xs text-subtle">{r.method}</span>
                  <span className="truncate text-fg">{r.eventType ?? t("logs.noEventType")}</span>
                  <span className="hidden truncate text-xs text-muted sm:block">{names.get(r.endpointId) ?? "—"}</span>
                  <span className="hidden text-right sm:block">{r.code != null ? <Badge tone={statusTone(r.code)}>{r.code}</Badge> : r.latency != null ? <span className="text-xs text-muted tabular-nums">{formatMs(locale, r.latency)}</span> : null}</span>
                  <span className="text-right text-xs whitespace-nowrap text-subtle">{formatRelative(locale, r.at)}</span>
                </button>
              </motion.li>
            ))}
          </AnimatePresence>
        </ul>
      )}
    </Card>
  );
}
