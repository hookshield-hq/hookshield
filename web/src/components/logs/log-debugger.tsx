"use client";

import { Fragment, useMemo, useState, type ReactNode } from "react";
import { useLocale, useTranslations } from "next-intl";
import { CheckCircle2, Circle, Clock, Database, Pencil, RotateCcw, ShieldCheck, Shuffle, XCircle, Inbox } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { useLog, useReplay, type Attempt, type LogDetail } from "@/lib/queries";
import { formatDateTime, formatMs, formatRelative } from "@/lib/format";
import { useErrorMessage } from "@/lib/use-error-message";
import { Badge, logTone, statusTone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CodeBlock, HeaderList, parseJSON, prettyJSON } from "@/components/ui/code";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty-state";
import { Textarea } from "@/components/ui/field";
import { Drawer, GlassModal } from "@/components/ui/overlay";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { useToast } from "@/components/ui/toast";
import { cn } from "@/lib/cn";

type Rendered = { body?: unknown; body_text?: string; body_base64?: string };

function Body({ value }: { value: Rendered }) {
  const t = useTranslations("logs.debugger");
  if (value.body !== undefined) return <CodeBlock value={value.body} json />;
  if (value.body_text !== undefined) return value.body_text ? <CodeBlock value={value.body_text} /> : <p className="text-xs text-subtle">{t("emptyBody")}</p>;
  if (value.body_base64 !== undefined)
    return (
      <div className="flex flex-col gap-1.5">
        <p className="text-xs text-subtle">{t("binaryBody")}</p>
        <CodeBlock value={value.body_base64} maxHeight="max-h-40" />
      </div>
    );
  return <p className="text-xs text-subtle">{t("emptyBody")}</p>;
}

function Section({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h4 className="text-xs font-semibold tracking-wide text-subtle uppercase">{title}</h4>
      {children}
    </section>
  );
}

function RequestView({ req }: { req: LogDetail["request"]["original"] }) {
  const t = useTranslations("logs.debugger");
  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-2 font-mono text-xs">
        <Badge tone="info">{req.method}</Badge>
        {req.query && <span className="truncate text-muted">?{req.query}</span>}
      </div>
      <Section title={t("headers")}>
        <HeaderList headers={req.headers} empty={t("noHeaders")} />
      </Section>
      <Section title={t("body")}>
        <Body value={req} />
      </Section>
    </div>
  );
}

function AttemptView({ attempts }: { attempts: Attempt[] }) {
  const t = useTranslations("logs.debugger");
  const errors = useTranslations("logs.attemptErrors");
  const locale = useLocale();
  const [n, setN] = useState(attempts.at(-1)?.number ?? 1);
  const a = attempts.find((x) => x.number === n) ?? attempts.at(-1);
  if (!a) return <EmptyState title={t("noAttempts")} body={t("noAttemptsBody")} />;
  return (
    <div className="flex flex-col gap-5">
      {attempts.length > 1 && (
        <div className="flex flex-wrap gap-1.5" role="group" aria-label={t("attempts")}>
          {attempts.map((x) => (
            <button
              key={x.number}
              type="button"
              aria-pressed={x.number === a.number}
              onClick={() => setN(x.number)}
              className={cn(
                "inline-flex h-7 items-center gap-1.5 rounded-md border px-2.5 text-xs transition",
                x.number === a.number ? "border-accent bg-hover text-fg" : "border-line text-muted hover:text-fg",
              )}
            >
              {t("attemptN", { n: x.number })}
              <span className={cn("size-1.5 rounded-full", x.status_code && x.status_code < 300 ? "bg-success" : "bg-danger")} />
            </button>
          ))}
        </div>
      )}
      <dl className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-3">
        <div>
          <dt className="text-xs text-subtle">{t("result")}</dt>
          <dd className="mt-1">
            {a.status_code != null ? (
              <Badge tone={statusTone(a.status_code)}>{a.status_code}</Badge>
            ) : (
              <Badge tone="danger">{errors.has(a.error_code ?? "") ? errors(a.error_code ?? "") : (a.error_code ?? "—")}</Badge>
            )}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-subtle">{t("duration")}</dt>
          <dd className="mt-1 text-fg tabular-nums">{a.duration_ms != null ? formatMs(locale, a.duration_ms) : "—"}</dd>
        </div>
        <div>
          <dt className="text-xs text-subtle">{t("startedAt")}</dt>
          <dd className="mt-1 text-xs text-fg">{formatDateTime(locale, a.started_at)}</dd>
        </div>
      </dl>
      {a.response ? (
        <>
          <Section title={t("responseHeaders")}>
            <HeaderList headers={a.response.headers} empty={t("noHeaders")} />
          </Section>
          <Section title={t("responseBody")}>
            <Body value={a.response} />
          </Section>
        </>
      ) : (
        <p className="text-xs text-muted">{t("noResponse")}</p>
      )}
    </div>
  );
}

type Step = { key: string; icon: ReactNode; title: ReactNode; detail?: ReactNode; tone: "ok" | "fail" | "wait" | "idle" };

/** Visual Flow Tracker: ingestion → authentication → transformation → storage → attempts (design §3.6). */
function FlowTimeline({ d }: { d: LogDetail }) {
  const t = useTranslations("logs.flow");
  const errors = useTranslations("logs.attemptErrors");
  const locale = useLocale();
  const l = d.log;
  const ms = (v: number | null | undefined) => (v != null ? formatMs(locale, v) : undefined);
  const steps: Step[] = [
    { key: "in", icon: <Inbox className="size-3.5" />, title: t("received"), detail: [l.latency.ingest_ms ? ms(l.latency.ingest_ms) : undefined, l.source_ip ?? (l.replay_of ? t("replay") : t("dashboard"))].filter(Boolean).join(" · "), tone: "ok" },
    { key: "auth", icon: <ShieldCheck className="size-3.5" />, title: t("authenticated"), detail: l.source_ip ? undefined : t("authSkipped"), tone: "ok" },
    {
      key: "tr",
      icon: <Shuffle className="size-3.5" />,
      title: d.request.transformed ? t("transformed") : t("notTransformed"),
      detail: d.request.transformed ? ms(l.latency.transform_ms) : undefined,
      tone: l.status === "queued" ? "wait" : "ok",
    },
    { key: "db", icon: <Database className="size-3.5" />, title: t("stored"), detail: ms(l.latency.persist_ms), tone: l.status === "queued" ? "wait" : "ok" },
  ];
  for (const a of d.attempts) {
    const ok = a.status_code != null && a.status_code >= 200 && a.status_code < 300;
    const result = a.status_code != null ? String(a.status_code) : errors.has(a.error_code ?? "") ? errors(a.error_code ?? "") : (a.error_code ?? "");
    steps.push({
      key: `a${a.number}`,
      icon: ok ? <CheckCircle2 className="size-3.5" /> : <XCircle className="size-3.5" />,
      title: a.number === 1 ? t("attempt", { result }) : t("retry", { n: a.number, result }),
      detail: ms(a.duration_ms),
      tone: ok ? "ok" : "fail",
    });
  }
  if (l.status === "retrying" && l.next_attempt_at)
    steps.push({ key: "next", icon: <Clock className="size-3.5" />, title: t("nextRetry"), detail: formatRelative(locale, l.next_attempt_at), tone: "wait" });
  if (l.status === "queued" || l.status === "processing") steps.push({ key: "wait", icon: <Circle className="size-3.5" />, title: t("waiting"), tone: "wait" });
  if (l.status === "dead") steps.push({ key: "dead", icon: <XCircle className="size-3.5" />, title: t("dead"), tone: "fail" });
  if (l.status === "failed") steps.push({ key: "failed", icon: <XCircle className="size-3.5" />, title: t("failed"), tone: "fail" });

  const toneCls = { ok: "bg-success-bg text-success", fail: "bg-danger-bg text-danger", wait: "bg-warning-bg text-warning", idle: "bg-hover text-subtle" };
  return (
    <ol className="flex flex-col" aria-label={t("label")}>
      {steps.map((s, i) => (
        <li key={s.key} className="relative flex gap-3 pb-4 last:pb-0">
          {i < steps.length - 1 && <span aria-hidden className="absolute top-7 bottom-0 left-3.5 w-px bg-line" />}
          <span className={cn("relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full", toneCls[s.tone])}>{s.icon}</span>
          <div className="flex min-w-0 flex-1 items-baseline justify-between gap-3 pt-1">
            <span className="text-sm text-fg">{s.title}</span>
            {s.detail && <span className="truncate text-xs text-muted tabular-nums">{s.detail}</span>}
          </div>
        </li>
      ))}
    </ol>
  );
}

function bodyToText(r: Rendered): { text: string; json: boolean } {
  if (r.body !== undefined) return { text: prettyJSON(r.body), json: true };
  return { text: r.body_text ?? "", json: false };
}

/** "Edit & replay": change the body, then send it through the pipeline again. */
function EditReplay({ d, open, onClose, onDone }: { d: LogDetail; open: boolean; onClose: () => void; onDone: (id: string) => void }) {
  const t = useTranslations("logs.debugger");
  const common = useTranslations("common");
  const errorMessage = useErrorMessage();
  const replay = useReplay();
  const initial = useMemo(() => bodyToText(d.request.original), [d.request.original]);
  const [text, setText] = useState(initial.text);
  const parsed = initial.json ? parseJSON(text) : { ok: true as const, value: undefined };
  const invalid = initial.json && !parsed.ok;
  return (
    <GlassModal
      open={open}
      onClose={onClose}
      title={t("editReplay")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {common("cancel")}
          </Button>
          <Button
            disabled={invalid}
            loading={replay.isPending}
            onClick={() =>
              replay.mutate(
                // A cleared JSON body is replayed as an empty body, not as "{}".
                { id: d.log.id, input: initial.json && parsed.ok && parsed.value !== undefined ? { body: parsed.value } : { body_text: initial.json ? "" : text } },
                { onSuccess: (r) => onDone(r.id) },
              )
            }
          >
            <RotateCcw className="size-4" />
            {t("replay")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="text-xs text-muted">{t("editReplayHint")}</p>
        <Textarea label={t("body")} mono rows={14} value={text} onChange={(e) => setText(e.target.value)} error={invalid ? t("invalidJson") : replay.error ? errorMessage(replay.error) : undefined} />
      </div>
    </GlassModal>
  );
}

/** Request debugger drawer (design §3.6): flow timeline, original/transformed request, destination responses, replay. */
export function LogDebugger({ id, onClose, onOpen }: { id: string | null; onClose: () => void; onOpen: (id: string) => void }) {
  const t = useTranslations("logs");
  const common = useTranslations("common");
  const locale = useLocale();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const q = useLog(id);
  const replay = useReplay();
  const [tab, setTab] = useState("original");
  const [editing, setEditing] = useState(false);
  const d = q.data;
  // A binary body cannot be edited as text: only the plain replay keeps it intact.
  const binary = d?.request.original.body_base64 != null;

  const replayed = (newId: string) => {
    setEditing(false);
    toast.success(t("debugger.replayed"));
    onOpen(newId);
  };

  return (
    <Drawer
      open={!!id}
      onClose={onClose}
      width="max-w-3xl"
      title={
        d ? (
          <span className="flex items-center gap-2">
            <Badge tone={logTone(d.log.status)}>{t(`status.${d.log.status}`)}</Badge>
            <span className="truncate">{d.log.event_type ?? t("noEventType")}</span>
          </span>
        ) : (
          t("debugger.title")
        )
      }
      footer={
        d && (
          <>
            {binary && (
              <p id="edit-replay-binary" className="mr-auto self-center text-xs text-muted">
                {t("debugger.binaryNoEdit")}
              </p>
            )}
            <Button variant="secondary" onClick={() => setEditing(true)} disabled={binary} aria-describedby={binary ? "edit-replay-binary" : undefined}>
              <Pencil className="size-4" />
              {t("debugger.editReplay")}
            </Button>
            <Button loading={replay.isPending} onClick={() => replay.mutate({ id: d.log.id }, { onSuccess: (r) => replayed(r.id), onError: (err) => toast.error(errorMessage(err)) })}>
              <RotateCcw className="size-4" />
              {t("debugger.replay")}
            </Button>
          </>
        )
      }
    >
      {q.isPending && id ? (
        <div className="flex flex-col gap-4" aria-busy="true">
          <Skeleton className="h-20 w-full" />
          <Skeleton className="h-48 w-full" />
          <span className="sr-only">{common("loading")}</span>
        </div>
      ) : q.isError ? (
        <EmptyState title={common("errorTitle")} body={errorMessage(q.error)} />
      ) : d ? (
        <div className="flex flex-col gap-6">
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-3">
            <div className="col-span-2 sm:col-span-3">
              <dt className="text-xs text-subtle">{t("debugger.id")}</dt>
              <dd className="mt-0.5 flex items-center gap-1 font-mono text-xs text-fg">
                {d.log.id}
                <CopyButton value={d.log.id} className="size-6" />
              </dd>
            </div>
            <div>
              <dt className="text-xs text-subtle">{t("columns.endpoint")}</dt>
              <dd className="mt-0.5 truncate">
                <Link href={`/endpoints/${d.log.endpoint.id}`} className="text-fg hover:text-accent">
                  {d.log.endpoint.name}
                </Link>
              </dd>
            </div>
            <div>
              <dt className="text-xs text-subtle">{t("columns.received")}</dt>
              <dd className="mt-0.5 text-xs text-fg" title={formatDateTime(locale, d.log.received_at)}>
                {formatRelative(locale, d.log.received_at)}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-subtle">{t("debugger.gateway")}</dt>
              <dd className="mt-0.5 text-xs text-fg tabular-nums">{d.log.latency.gateway_ms != null ? formatMs(locale, d.log.latency.gateway_ms) : "—"}</dd>
            </div>
            {d.log.replay_of && (
              <div className="col-span-2 sm:col-span-3">
                <dt className="text-xs text-subtle">{t("debugger.replayOf")}</dt>
                <dd className="mt-0.5">
                  <button type="button" onClick={() => onOpen(d.log.replay_of!)} className="font-mono text-xs text-accent hover:underline">
                    {d.log.replay_of}
                  </button>
                </dd>
              </div>
            )}
            {d.replays.length > 0 && (
              <div className="col-span-2 sm:col-span-3">
                <dt className="text-xs text-subtle">{t("debugger.replays")}</dt>
                <dd className="mt-0.5 flex flex-wrap gap-x-3 gap-y-1">
                  {d.replays.map((r) => (
                    <Fragment key={r}>
                      <button type="button" onClick={() => onOpen(r)} className="font-mono text-xs text-accent hover:underline">
                        {r.slice(0, 13)}…
                      </button>
                    </Fragment>
                  ))}
                </dd>
              </div>
            )}
          </dl>

          <section className="rounded-xl border border-line bg-surface-strong/40 p-4">
            <h3 className="mb-3 text-xs font-semibold tracking-wide text-subtle uppercase">{t("flow.label")}</h3>
            <FlowTimeline d={d} />
          </section>

          <Tabs
            label={t("debugger.inspect")}
            value={tab}
            onChange={setTab}
            tabs={[
              { id: "original", label: t("debugger.original"), content: <RequestView req={d.request.original} /> },
              {
                id: "transformed",
                label: t("debugger.transformed"),
                content: d.request.transformed ? <RequestView req={d.request.transformed} /> : <EmptyState title={t("debugger.notTransformed")} body={t("debugger.notTransformedBody")} />,
              },
              { id: "response", label: t("debugger.response", { n: d.attempts.length }), content: <AttemptView key={d.attempts.length} attempts={d.attempts} /> },
            ]}
          />
          {editing && !binary && <EditReplay d={d} open={editing} onClose={() => setEditing(false)} onDone={replayed} />}
        </div>
      ) : null}
    </Drawer>
  );
}
