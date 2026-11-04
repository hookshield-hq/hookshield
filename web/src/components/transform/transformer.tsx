"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "next/navigation";
import { useTranslations } from "next-intl";
import { ArrowDown, ArrowUp, Braces, Download, FileJson, Plus, Save, Shuffle, Trash2, Wand2 } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { ApiError } from "@/lib/api/errors";
import { api, data } from "@/lib/api/client";
import { useEndpoints, useLogs, usePreviewTransform, useUpdateEndpoint, type Endpoint, type TransformRules } from "@/lib/queries";
import { useErrorMessage } from "@/lib/use-error-message";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock, HeaderList, parseJSON, prettyJSON } from "@/components/ui/code";
import { EmptyState } from "@/components/ui/empty-state";
import { Select, Textarea } from "@/components/ui/field";
import { PageHeader } from "@/components/ui/page-header";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { cn } from "@/lib/cn";
import { BODY_OPS, fromRules, HEADER_OPS, newBody, newHeader, toRules, type BodyDraft, type BodyOp, type HeaderDraft, type HeaderOp } from "./rules";

const SAMPLE_BODY = {
  id: "evt_1Q2w3E",
  type: "invoice.paid",
  user_id: 42,
  card: { brand: "visa", last4: "4242" },
  data: { object: { id: "in_9", amount_paid: 4900, currency: "usd" } },
};
const SAMPLE_HEADERS = { "Content-Type": "application/json", "Stripe-Signature": "t=1700000000,v1=5257a8…", "User-Agent": "Stripe/1.0" };

type Problem = { section: string; index: number; code: string };

/** The engine's warnings are fixed English strings (see backend/internal/transform): translate the known shapes. */
function describeWarning(t: ReturnType<typeof useTranslations>, w: string): string {
  const missing = /^body\[(\d+)\] path not found: (.*)$/.exec(w);
  if (missing) return t("warnings.pathNotFound", { n: Number(missing[1]) + 1, path: missing[2] });
  if (w.includes("not a JSON object")) return t("warnings.notJson");
  return t("warnings.other");
}

const field =
  "h-8 w-full min-w-0 rounded-md border border-line bg-surface-strong/60 px-2 font-mono text-xs text-fg outline-none transition placeholder:text-subtle focus:border-accent focus:ring-2 focus:ring-accent/25";

function Mini({ label, value, onChange, placeholder, className }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string; className?: string }) {
  return (
    <label className={cn("flex min-w-0 flex-col gap-1", className)}>
      <span className="text-[11px] text-subtle">{label}</span>
      <input value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} className={field} />
    </label>
  );
}

function RuleShell({ index, total, title, onMove, onRemove, error, children }: { index: number; total: number; title: ReactNode; onMove: (d: -1 | 1) => void; onRemove: () => void; error?: string; children: ReactNode }) {
  const t = useTranslations("transform");
  return (
    <li className={cn("rounded-lg border bg-surface-strong/40 p-3", error ? "border-danger/60" : "border-line")}>
      <div className="mb-2 flex items-center gap-2">
        <span className="flex size-5 items-center justify-center rounded bg-hover text-[11px] font-medium text-muted tabular-nums">{index + 1}</span>
        <span className="flex-1 text-xs font-medium text-fg">{title}</span>
        <button type="button" disabled={index === 0} onClick={() => onMove(-1)} aria-label={t("moveUp")} className="rounded p-1 text-subtle transition hover:text-fg disabled:opacity-30">
          <ArrowUp className="size-3.5" />
        </button>
        <button type="button" disabled={index === total - 1} onClick={() => onMove(1)} aria-label={t("moveDown")} className="rounded p-1 text-subtle transition hover:text-fg disabled:opacity-30">
          <ArrowDown className="size-3.5" />
        </button>
        <button type="button" onClick={onRemove} aria-label={t("removeRule")} className="rounded p-1 text-subtle transition hover:text-danger">
          <Trash2 className="size-3.5" />
        </button>
      </div>
      <div className="grid grid-cols-2 gap-2">{children}</div>
      {error && (
        <p role="alert" className="mt-2 text-xs text-danger">
          {error}
        </p>
      )}
    </li>
  );
}

function move<T>(list: T[], i: number, d: -1 | 1): T[] {
  const j = i + d;
  if (j < 0 || j >= list.length) return list;
  const out = [...list];
  [out[i], out[j]] = [out[j], out[i]];
  return out;
}

function AddMenu<T extends string>({ label, ops, onAdd, opLabel }: { label: string; ops: T[]; onAdd: (op: T) => void; opLabel: (op: T) => string }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="mr-1 inline-flex items-center gap-1 text-xs text-muted">
        <Plus className="size-3.5" />
        {label}
      </span>
      {ops.map((op) => (
        <button key={op} type="button" onClick={() => onAdd(op)} className="h-7 rounded-md border border-dashed border-line px-2.5 text-xs text-muted transition hover:border-accent hover:text-fg">
          {opLabel(op)}
        </button>
      ))}
    </div>
  );
}

function EndpointEditor({ endpoint }: { endpoint: Endpoint }) {
  const t = useTranslations("transform");
  const ex = useTranslations("examples");
  const errors = useTranslations("errors");
  const errorMessage = useErrorMessage();
  const toast = useToast();
  const update = useUpdateEndpoint(endpoint.id);
  const preview = usePreviewTransform();
  const latest = useLogs({ endpoint_id: endpoint.id }, 1);
  const initial = useMemo(() => fromRules(endpoint.transform_rules as TransformRules), [endpoint.transform_rules]);
  const [body, setBody] = useState<BodyDraft[]>(initial.body);
  const [headers, setHeaders] = useState<HeaderDraft[]>(initial.headers);
  const [mode, setMode] = useState<"blocks" | "json">("blocks");
  const [rawRules, setRawRules] = useState("");
  const [sampleBody, setSampleBody] = useState(prettyJSON(SAMPLE_BODY));
  const [sampleHeaders, setSampleHeaders] = useState(prettyJSON(SAMPLE_HEADERS));
  const [loadingSample, setLoadingSample] = useState(false);

  const rules = useMemo(() => toRules(body, headers), [body, headers]);
  const saved = JSON.stringify(toRules(initial.body, initial.headers));
  const dirty = JSON.stringify(rules) !== saved;
  const parsedBody = parseJSON(sampleBody);
  const parsedHeaders = parseJSON(sampleHeaders);

  // Live preview, debounced: runs exactly the worker's code on the sample.
  const { mutate: runPreview } = preview;
  useEffect(() => {
    if (!parsedHeaders.ok) return;
    const id = setTimeout(() => {
      const request = parsedBody.ok
        ? { headers: (parsedHeaders.value ?? {}) as Record<string, string>, body: parsedBody.value ?? {} }
        : { headers: (parsedHeaders.value ?? {}) as Record<string, string>, body_text: sampleBody };
      runPreview({ rules, request });
    }, 300);
    return () => clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [JSON.stringify(rules), sampleBody, sampleHeaders, runPreview]);

  const problems: Problem[] = preview.error instanceof ApiError && Array.isArray(preview.error.details) ? (preview.error.details as Problem[]) : [];
  const problemFor = (section: string, index: number) => {
    const p = problems.find((x) => x.section === section && x.index === index);
    return p ? (errors.has(p.code) ? errors(p.code) : errors("unknown")) : undefined;
  };
  const generalProblems = problems.filter((p) => p.section === "rules");

  async function loadLatest() {
    const log = latest.data?.pages[0]?.logs[0];
    if (!log) return;
    setLoadingSample(true);
    try {
      const d = data(await (await api()).GET("/v1/logs/{id}", { params: { path: { id: log.id } } }));
      const o = d.request.original;
      setSampleBody(o.body !== undefined ? prettyJSON(o.body) : (o.body_text ?? ""));
      setSampleHeaders(prettyJSON(Object.fromEntries(Object.entries(o.headers).map(([k, v]) => [k, v.length === 1 ? v[0] : v]))));
      toast.success(t("sampleLoaded"));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setLoadingSample(false);
    }
  }

  function switchMode(m: "blocks" | "json") {
    if (m === "json") setRawRules(rules ? prettyJSON(rules) : "");
    else {
      const parsed = parseJSON(rawRules);
      if (!parsed.ok) {
        toast.error(t("invalidRulesJson"));
        return;
      }
      const d = fromRules((parsed.value ?? null) as TransformRules | null);
      setBody(d.body);
      setHeaders(d.headers);
    }
    setMode(m);
  }

  const save = () => {
    let toSave = rules;
    if (mode === "json") {
      const parsed = parseJSON(rawRules);
      if (!parsed.ok) {
        toast.error(t("invalidRulesJson"));
        return;
      }
      toSave = (parsed.value ?? null) as TransformRules | null;
    }
    update.mutate({ transform_rules: toSave }, { onSuccess: () => toast.success(t("saved")), onError: (err) => toast.error(errorMessage(err)) });
  };

  const setB = (i: number, patch: Partial<BodyDraft>) => setBody(body.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const setH = (i: number, patch: Partial<HeaderDraft>) => setHeaders(headers.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const out = preview.data?.request as { headers?: Record<string, string[]>; body?: unknown; body_text?: string } | undefined;

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <p className="text-xs text-muted">{dirty ? t("unsaved") : t("savedState")}</p>
        <div className="flex items-center gap-2">
          <Segmented
            size="sm"
            label={t("editorMode")}
            value={mode}
            onChange={switchMode}
            options={[
              { value: "blocks", label: t("blocks"), icon: <Wand2 className="size-3.5" /> },
              { value: "json", label: t("json"), icon: <Braces className="size-3.5" /> },
            ]}
          />
          <Button onClick={save} loading={update.isPending} disabled={mode === "blocks" && !dirty}>
            <Save className="size-4" />
            {t("save")}
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
        {/* input */}
        <Card className="flex flex-col">
          <CardHeader>
            <div>
              <CardTitle>{t("input")}</CardTitle>
              <p className="mt-0.5 text-xs text-muted">{t("inputHint")}</p>
            </div>
            <Button variant="secondary" size="sm" onClick={loadLatest} loading={loadingSample} disabled={!latest.data?.pages[0]?.logs.length} title={latest.data?.pages[0]?.logs.length ? undefined : t("noLatest")}>
              <Download className="size-3.5" />
              {t("loadLatest")}
            </Button>
          </CardHeader>
          <CardBody className="flex flex-1 flex-col gap-4">
            <Textarea label={t("sampleHeaders")} mono rows={6} value={sampleHeaders} onChange={(e) => setSampleHeaders(e.target.value)} error={parsedHeaders.ok ? undefined : t("invalidJson")} />
            <Textarea label={t("sampleBody")} mono rows={16} value={sampleBody} onChange={(e) => setSampleBody(e.target.value)} hint={parsedBody.ok ? undefined : t("bodyNotJson")} />
          </CardBody>
        </Card>

        {/* pipeline */}
        <Card className="flex flex-col">
          <CardHeader>
            <div>
              <CardTitle>{t("pipeline")}</CardTitle>
              <p className="mt-0.5 text-xs text-muted">{t("pipelineHint")}</p>
            </div>
          </CardHeader>
          <CardBody className="flex flex-1 flex-col gap-5">
            {mode === "json" ? (
              <Textarea label={t("rulesJson")} mono rows={24} value={rawRules} onChange={(e) => setRawRules(e.target.value)} hint={t("rulesJsonHint")} />
            ) : (
              <>
                <section className="flex flex-col gap-2">
                  <h3 className="text-xs font-semibold tracking-wide text-subtle uppercase">{t("sections.body")}</h3>
                  {body.length > 0 && (
                    <ol className="flex flex-col gap-2">
                      {body.map((r, i) => (
                        <RuleShell key={r.id} index={i} total={body.length} title={t(`ops.${r.op}`)} onMove={(d) => setBody(move(body, i, d))} onRemove={() => setBody(body.filter((_, j) => j !== i))} error={problemFor("body", i)}>
                          {(r.op === "remove" || r.op === "set" || r.op === "set_timestamp") && <Mini label={t("fields.path")} value={r.path} onChange={(v) => setB(i, { path: v })} placeholder={ex("path")} className={r.op === "remove" ? "col-span-2" : ""} />}
                          {(r.op === "rename" || r.op === "copy") && (
                            <>
                              <Mini label={t("fields.from")} value={r.from} onChange={(v) => setB(i, { from: v })} placeholder={ex("from")} />
                              <Mini label={t("fields.to")} value={r.to} onChange={(v) => setB(i, { to: v })} placeholder={ex("to")} />
                            </>
                          )}
                          {r.op === "set" && <Mini label={t("fields.value")} value={r.valueText} onChange={(v) => setB(i, { valueText: v })} placeholder={t("fields.valuePlaceholder")} />}
                          {r.op === "set_timestamp" && (
                            <label className="flex min-w-0 flex-col gap-1">
                              <span className="text-[11px] text-subtle">{t("fields.format")}</span>
                              <select value={r.format} onChange={(e) => setB(i, { format: e.target.value as BodyDraft["format"] })} className={field}>
                                <option value="rfc3339">{t("formats.rfc3339")}</option>
                                <option value="unix">{t("formats.unix")}</option>
                                <option value="unix_ms">{t("formats.unix_ms")}</option>
                              </select>
                            </label>
                          )}
                          {r.op === "keep" && <Mini label={t("fields.paths")} value={r.pathsText} onChange={(v) => setB(i, { pathsText: v })} placeholder={ex("paths")} className="col-span-2" />}
                        </RuleShell>
                      ))}
                    </ol>
                  )}
                  <AddMenu label={t("addBody")} ops={BODY_OPS} opLabel={(op: BodyOp) => t(`ops.${op}`)} onAdd={(op) => setBody([...body, newBody(op)])} />
                </section>
                <section className="flex flex-col gap-2 border-t border-line pt-4">
                  <h3 className="text-xs font-semibold tracking-wide text-subtle uppercase">{t("sections.headers")}</h3>
                  {headers.length > 0 && (
                    <ol className="flex flex-col gap-2">
                      {headers.map((r, i) => (
                        <RuleShell key={r.id} index={i} total={headers.length} title={t(`headerOps.${r.op}`)} onMove={(d) => setHeaders(move(headers, i, d))} onRemove={() => setHeaders(headers.filter((_, j) => j !== i))} error={problemFor("headers", i)}>
                          {(r.op === "remove" || r.op === "set") && <Mini label={t("fields.header")} value={r.name} onChange={(v) => setH(i, { name: v })} placeholder={ex("header")} className={r.op === "remove" ? "col-span-2" : ""} />}
                          {r.op === "set" && <Mini label={t("fields.value")} value={r.value} onChange={(v) => setH(i, { value: v })} placeholder={ex("headerValue")} />}
                          {r.op === "rename" && (
                            <>
                              <Mini label={t("fields.from")} value={r.from} onChange={(v) => setH(i, { from: v })} placeholder={ex("headerFrom")} />
                              <Mini label={t("fields.to")} value={r.to} onChange={(v) => setH(i, { to: v })} placeholder={ex("headerTo")} />
                            </>
                          )}
                        </RuleShell>
                      ))}
                    </ol>
                  )}
                  <AddMenu label={t("addHeader")} ops={HEADER_OPS} opLabel={(op: HeaderOp) => t(`headerOps.${op}`)} onAdd={(op) => setHeaders([...headers, newHeader(op)])} />
                </section>
                <p className="text-xs text-subtle">
                  {t.rich("secretsNote", {
                    link: (c) => (
                      <Link href={`/endpoints/${endpoint.id}`} className="text-accent underline underline-offset-2 hover:decoration-2">
                        {c}
                      </Link>
                    ),
                  })}
                </p>
              </>
            )}
            {generalProblems.length > 0 && (
              <p role="alert" className="text-xs text-danger">
                {generalProblems.map((p) => (errors.has(p.code) ? errors(p.code) : errors("unknown"))).join(" · ")}
              </p>
            )}
          </CardBody>
        </Card>

        {/* output */}
        <Card className="flex flex-col">
          <CardHeader>
            <div>
              <CardTitle>{t("output")}</CardTitle>
              <p className="mt-0.5 text-xs text-muted">{t("outputHint")}</p>
            </div>
            {preview.isPending && <Badge tone="info">{t("running")}</Badge>}
          </CardHeader>
          <CardBody className="flex flex-1 flex-col gap-4">
            {preview.isError && problems.length === 0 ? (
              <p role="alert" className="text-sm text-danger">
                {errorMessage(preview.error)}
              </p>
            ) : problems.length > 0 ? (
              <EmptyState icon={<Shuffle className="size-6" />} title={t("fixRules")} body={t("fixRulesBody")} />
            ) : out ? (
              <>
                <div className="flex flex-col gap-1.5">
                  <span className="text-xs font-medium text-muted">{t("outHeaders")}</span>
                  <HeaderList headers={out.headers} empty={t("noHeaders")} />
                </div>
                <div className="flex flex-col gap-1.5">
                  <span className="text-xs font-medium text-muted">{t("outBody")}</span>
                  {out.body !== undefined ? <CodeBlock value={out.body} json maxHeight="max-h-[28rem]" /> : <CodeBlock value={out.body_text ?? ""} />}
                </div>
                {(preview.data?.warnings?.length ?? 0) > 0 && (
                  <ul className="flex flex-col gap-1 text-xs text-warning">
                    {preview.data!.warnings!.map((w, i) => (
                      <li key={i}>{describeWarning(t, String(w))}</li>
                    ))}
                  </ul>
                )}
              </>
            ) : (
              <Skeleton className="h-64 w-full" />
            )}
          </CardBody>
        </Card>
      </div>
    </>
  );
}

/** Payload transformer (design §3.4): input sample, rule pipeline and live output preview. */
export function Transformer() {
  const t = useTranslations("transform");
  const params = useSearchParams();
  const endpoints = useEndpoints();
  const [chosen, setChosen] = useState<string | null>(params.get("endpoint"));
  const list = endpoints.data ?? [];
  const endpoint = list.find((e) => e.id === chosen) ?? list[0];

  return (
    <div className="mx-auto max-w-[96rem]">
      <PageHeader
        title={t("title")}
        description={t("subtitle")}
        actions={
          list.length > 0 && (
            <div className="w-64">
              <Select aria-label={t("endpoint")} value={endpoint?.id ?? ""} onChange={(e) => setChosen(e.target.value)}>
                {list.map((e) => (
                  <option key={e.id} value={e.id}>
                    {e.name}
                  </option>
                ))}
              </Select>
            </div>
          )
        }
      />
      {endpoints.isPending ? (
        <Skeleton className="h-96 w-full" />
      ) : !endpoint ? (
        <Card>
          <EmptyState
            icon={<FileJson className="size-6" />}
            title={t("noEndpoints")}
            body={t("noEndpointsBody")}
            action={
              <Link href="/endpoints" className="bg-accent-gradient inline-flex h-9 items-center rounded-lg px-4 text-sm font-medium text-white">
                {t("goEndpoints")}
              </Link>
            }
          />
        </Card>
      ) : (
        <EndpointEditor key={endpoint.id} endpoint={endpoint} />
      )}
    </div>
  );
}
