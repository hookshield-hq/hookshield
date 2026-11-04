"use client";

import { useState, type FormEvent, type ReactNode } from "react";
import { useTranslations } from "next-intl";
import { AlertTriangle, Bell, Mail, MessageSquare, Plus, RefreshCw, Send, Shuffle, Webhook } from "lucide-react";
import { Link } from "@/i18n/navigation";
import {
  useAlertChannels,
  useRotateSecret,
  useUpdateEndpoint,
  type AlertChannel,
  type Endpoint,
  type EndpointInput,
  type EndpointResponse,
  type TransformRules,
} from "@/lib/queries";
import type { RetryPolicy } from "@/lib/duration";
import { useErrorMessage, useFieldErrors } from "@/lib/use-error-message";
import { FormError } from "@/components/auth/fields";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ui/confirm";
import { Textarea } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { fromKV, KeyValueEditor, toKV, type KV } from "@/components/ui/kv-editor";
import { Segmented } from "@/components/ui/segmented";
import { Toggle } from "@/components/ui/toggle";
import { useToast } from "@/components/ui/toast";
import { RetryEditor } from "./retry-editor";
import { AuthFields, authDraftFrom, authInput, endpointFieldOf, SecretReveal, type AuthDraft } from "./shared";

/** Card with a form and a save button that only lights up after a change. */
function FormCard({ title, description, children, onSubmit, dirty, saving, error, id }: { title: ReactNode; description?: ReactNode; children: ReactNode; onSubmit: () => void; dirty: boolean; saving: boolean; error?: string; id: string }) {
  const common = useTranslations("common");
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{title}</CardTitle>
          {description && <p className="mt-0.5 text-xs text-muted">{description}</p>}
        </div>
      </CardHeader>
      <CardBody>
        <form
          id={id}
          noValidate
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            onSubmit();
          }}
          className="flex flex-col gap-5"
        >
          {children}
          <FormError message={error} />
          <div className="flex justify-end">
            <Button type="submit" disabled={!dirty} loading={saving}>
              {common("saveChanges")}
            </Button>
          </div>
        </form>
      </CardBody>
    </Card>
  );
}

function useSave(endpoint: Endpoint) {
  const t = useTranslations("endpoints.detail");
  const toast = useToast();
  const update = useUpdateEndpoint(endpoint.id);
  const errors = useFieldErrors(update.error, endpointFieldOf);
  const save = (body: EndpointInput, then?: (r: EndpointResponse) => void) =>
    update.mutate(body, {
      onSuccess: (r) => {
        toast.success(t("saved"));
        then?.(r);
      },
    });
  return { update, save, ...errors };
}

// ── general ──

export function GeneralSection({ endpoint }: { endpoint: Endpoint }) {
  const t = useTranslations("endpoints");
  const { update, save, fieldError, formError } = useSave(endpoint);
  const initial = { name: endpoint.name, description: endpoint.description, target: endpoint.target_url, status: endpoint.status };
  const [form, setForm] = useState(initial);
  const dirty = JSON.stringify(form) !== JSON.stringify(initial);
  return (
    <FormCard
      id="endpoint-general"
      title={t("detail.general")}
      description={t("detail.generalHint")}
      dirty={dirty}
      saving={update.isPending}
      error={formError}
      onSubmit={() => save({ name: form.name.trim(), description: form.description.trim(), target_url: form.target.trim(), status: form.status })}
    >
      <Input label={t("create.name")} value={form.name} maxLength={100} onChange={(e) => setForm({ ...form, name: e.target.value })} error={fieldError("name")} />
      <Input label={t("create.target")} type="url" value={form.target} onChange={(e) => setForm({ ...form, target: e.target.value })} error={fieldError("target_url")} hint={t("create.targetHint")} className="font-mono" />
      <Textarea label={t("create.description")} rows={2} maxLength={1000} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} error={fieldError("description")} />
      <div className="flex flex-col gap-1.5">
        <span className="text-xs font-medium text-muted">{t("detail.statusLabel")}</span>
        <Segmented
          label={t("detail.statusLabel")}
          value={form.status}
          onChange={(status) => setForm({ ...form, status })}
          options={[
            { value: "active", label: t("status.active") },
            { value: "paused", label: t("status.paused") },
            { value: "debug", label: t("status.debug") },
          ]}
        />
        <p className="text-xs text-subtle">{t(`detail.statusHint.${form.status}`)}</p>
      </div>
    </FormCard>
  );
}

// ── security ──

export function SecuritySection({ endpoint }: { endpoint: Endpoint }) {
  const t = useTranslations("endpoints");
  const errorMessage = useErrorMessage();
  const toast = useToast();
  const { update, save, fieldError, formError } = useSave(endpoint);
  const rotate = useRotateSecret(endpoint.id);
  const [draft, setDraft] = useState<AuthDraft>(() => authDraftFrom(endpoint));
  const [confirmRotate, setConfirmRotate] = useState(false);
  const [revealed, setRevealed] = useState<EndpointResponse | null>(null);
  const initial = authDraftFrom(endpoint);
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial);
  const a = endpoint.auth;

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("detail.currentAuth")}</CardTitle>
            <p className="mt-0.5 text-xs text-muted">{t(`auth.${a.mode === "ip_allowlist" ? "ip" : a.mode}Hint`)}</p>
          </div>
          {a.mode !== "ip_allowlist" && (
            <Button variant="secondary" size="sm" onClick={() => setConfirmRotate(true)}>
              <RefreshCw className="size-3.5" />
              {a.mode === "token" ? t("detail.rotateToken") : t("detail.rotateHmac")}
            </Button>
          )}
        </CardHeader>
        <CardBody>
          <dl className="grid grid-cols-1 gap-4 text-sm sm:grid-cols-3">
            <div>
              <dt className="text-xs text-subtle">{t("auth.mode")}</dt>
              <dd className="mt-1 text-fg">{t(`auth.${a.mode === "ip_allowlist" ? "ip" : a.mode}`)}</dd>
            </div>
            {a.mode === "token" && (
              <>
                <div>
                  <dt className="text-xs text-subtle">{t("auth.header")}</dt>
                  <dd className="mt-1 font-mono text-xs text-fg">{a.header}</dd>
                </div>
                <div>
                  <dt className="text-xs text-subtle">{t("detail.tokenPrefix")}</dt>
                  <dd className="mt-1 font-mono text-xs text-fg">{a.token_prefix ? `${a.token_prefix}…` : "—"}</dd>
                </div>
              </>
            )}
            {a.mode === "hmac" && (
              <div>
                <dt className="text-xs text-subtle">{t("auth.hmacHeader")}</dt>
                <dd className="mt-1 font-mono text-xs text-fg">{a.hmac_header}</dd>
              </div>
            )}
            {a.mode === "ip_allowlist" && (
              <div className="sm:col-span-2">
                <dt className="text-xs text-subtle">{t("auth.ips")}</dt>
                <dd className="mt-1 flex flex-wrap gap-1">
                  {a.ip_allowlist.map((ip) => (
                    <Badge key={ip} className="font-mono">
                      {ip}
                    </Badge>
                  ))}
                </dd>
              </div>
            )}
          </dl>
        </CardBody>
      </Card>

      <FormCard
        id="endpoint-auth"
        title={t("detail.changeAuth")}
        description={t("detail.changeAuthHint")}
        dirty={dirty}
        saving={update.isPending}
        error={formError}
        onSubmit={() =>
          save({ auth: authInput(draft) }, (r) => {
            setDraft(authDraftFrom(r.endpoint));
            if (r.secrets?.token || r.secrets?.hmac_secret) setRevealed(r);
          })
        }
      >
        <AuthFields draft={draft} onChange={setDraft} fieldError={fieldError} />
      </FormCard>

      <ConfirmDialog
        open={confirmRotate}
        onClose={() => setConfirmRotate(false)}
        title={a.mode === "token" ? t("detail.rotateToken") : t("detail.rotateHmac")}
        body={t("detail.rotateBody")}
        confirmLabel={t("detail.rotate")}
        loading={rotate.isPending}
        onConfirm={() =>
          rotate.mutate(undefined, {
            onSuccess: (r) => {
              setConfirmRotate(false);
              setRevealed(r);
            },
            onError: (err) => {
              setConfirmRotate(false);
              toast.error(errorMessage(err));
            },
          })
        }
      />
      <SecretReveal response={revealed} title={t("detail.newSecret")} onClose={() => setRevealed(null)} />
    </div>
  );
}

// ── delivery: outbound headers, retry policy, DLQ ──

export function DeliverySection({ endpoint, warnings }: { endpoint: Endpoint; warnings: { code: string; secret?: string }[] }) {
  const t = useTranslations("endpoints");
  const { update, save, fieldError, formError } = useSave(endpoint);
  const initial = { headers: toKV(endpoint.outbound_headers), retry: endpoint.retry as RetryPolicy, dlq: endpoint.dlq_enabled };
  const [headers, setHeaders] = useState<KV[]>(initial.headers);
  const [retry, setRetry] = useState<RetryPolicy>(initial.retry);
  const [dlq, setDlq] = useState(initial.dlq);
  const dirty = JSON.stringify(fromKV(headers)) !== JSON.stringify(endpoint.outbound_headers) || JSON.stringify(retry) !== JSON.stringify(endpoint.retry) || dlq !== endpoint.dlq_enabled;
  const missing = warnings.filter((w) => w.code === "endpoint.secret_missing" && w.secret);

  return (
    <FormCard
      id="endpoint-delivery"
      title={t("detail.delivery")}
      description={t("detail.deliveryHint")}
      dirty={dirty}
      saving={update.isPending}
      error={formError}
      onSubmit={() =>
        save({
          outbound_headers: fromKV(headers),
          retry: { ...retry, custom_schedule_ms: retry.strategy === "custom" ? retry.custom_schedule_ms : [] },
          dlq_enabled: dlq,
        })
      }
    >
      <section className="flex flex-col gap-3">
        <div>
          <h3 className="text-sm font-semibold text-fg">{t("detail.outboundHeaders")}</h3>
          <p className="mt-0.5 text-xs text-muted">{t.rich("detail.outboundHint", { ref: "{{secrets.NAME}}", code: (c) => <code className="font-mono text-accent">{c}</code> })}</p>
        </div>
        <KeyValueEditor rows={headers} onChange={setHeaders} keyPlaceholder={t("detail.headerName")} valuePlaceholder={t("detail.headerValue")} addLabel={t("detail.addHeader")} />
        {fieldError("outbound_headers") && (
          <p role="alert" className="text-xs text-danger">
            {fieldError("outbound_headers")}
          </p>
        )}
        {missing.length > 0 && (
          <div className="flex items-start gap-2 rounded-lg bg-warning-bg p-3 text-xs text-warning">
            <AlertTriangle className="mt-0.5 size-4 shrink-0" />
            <p>
              {t.rich("detail.missingSecrets", {
                names: missing.map((w) => w.secret).join(", "),
                link: (c) => (
                  <Link href="/secrets" className="font-medium underline">
                    {c}
                  </Link>
                ),
              })}
            </p>
          </div>
        )}
      </section>
      <section className="flex flex-col gap-3 border-t border-line pt-5">
        <div>
          <h3 className="text-sm font-semibold text-fg">{t("retry.title")}</h3>
          <p className="mt-0.5 text-xs text-muted">{t("retry.subtitle")}</p>
        </div>
        <RetryEditor value={retry} onChange={setRetry} fieldError={fieldError} />
      </section>
      <section className="border-t border-line pt-5">
        <Toggle checked={dlq} onChange={setDlq} label={t("detail.dlq")} description={t("detail.dlqHint")} />
      </section>
    </FormCard>
  );
}

// ── transform rules summary ──

export function describeRule(t: ReturnType<typeof useTranslations>, section: "body" | "headers", r: Record<string, unknown>): string {
  const s = (k: string) => String(r[k] ?? "");
  if (section === "headers") {
    if (r.op === "remove") return t("transform.describe.headerRemove", { name: s("name") });
    if (r.op === "rename") return t("transform.describe.headerRename", { from: s("from"), to: s("to") });
    return t("transform.describe.headerSet", { name: s("name"), value: s("value") });
  }
  switch (r.op) {
    case "remove":
      return t("transform.describe.remove", { path: s("path") });
    case "rename":
      return t("transform.describe.rename", { from: s("from"), to: s("to") });
    case "copy":
      return t("transform.describe.copy", { from: s("from"), to: s("to") });
    case "set":
      return t("transform.describe.set", { path: s("path"), value: JSON.stringify(r.value) });
    case "set_timestamp":
      return t("transform.describe.timestamp", { path: s("path") });
    case "keep":
      return t("transform.describe.keep", { paths: ((r.paths as string[]) ?? []).join(", ") });
    default:
      return String(r.op);
  }
}

export function TransformSummary({ endpoint }: { endpoint: Endpoint }) {
  const t = useTranslations();
  const rules = (endpoint.transform_rules ?? {}) as TransformRules;
  const body = rules.body ?? [];
  const headers = rules.headers ?? [];
  const total = body.length + headers.length;
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("endpoints.detail.transform")}</CardTitle>
          <p className="mt-0.5 text-xs text-muted">{total ? t("endpoints.detail.rulesCount", { n: total }) : t("endpoints.detail.noRules")}</p>
        </div>
        <Link href={`/transform?endpoint=${endpoint.id}`} className="glass inline-flex h-8 items-center gap-1.5 rounded-lg px-3 text-xs font-medium text-fg transition hover:bg-hover">
          <Shuffle className="size-3.5" />
          {t("endpoints.detail.openTransformer")}
        </Link>
      </CardHeader>
      {total > 0 && (
        <CardBody>
          <ol className="flex flex-col gap-2 text-sm">
            {body.map((r, i) => (
              <li key={`b${i}`} className="flex items-center gap-2">
                <Badge tone="info">{t("transform.sections.body")}</Badge>
                <span className="font-mono text-xs text-fg">{describeRule(t, "body", r as Record<string, unknown>)}</span>
              </li>
            ))}
            {headers.map((r, i) => (
              <li key={`h${i}`} className="flex items-center gap-2">
                <Badge>{t("transform.sections.headers")}</Badge>
                <span className="font-mono text-xs text-fg">{describeRule(t, "headers", r as Record<string, unknown>)}</span>
              </li>
            ))}
          </ol>
        </CardBody>
      )}
    </Card>
  );
}

// ── alerts ──

export const channelIcon: Record<AlertChannel["type"], ReactNode> = {
  webhook: <Webhook className="size-4" />,
  slack: <MessageSquare className="size-4" />,
  telegram: <Send className="size-4" />,
  email: <Mail className="size-4" />,
};

export function channelTarget(c: AlertChannel): string {
  return c.target.to || c.target.url || c.target.webhook_url || [c.target.chat_id, c.target.bot_token].filter(Boolean).join(" · ") || "—";
}

export function AlertsSection({ endpoint }: { endpoint: Endpoint }) {
  const t = useTranslations();
  const { update, save, fieldError, formError } = useSave(endpoint);
  const channels = useAlertChannels();
  const [threshold, setThreshold] = useState(endpoint.alert.threshold_failures);
  const applying = (channels.data ?? []).filter((c) => c.endpoint_id === null || c.endpoint_id === endpoint.id);
  return (
    <div className="flex flex-col gap-6">
      <FormCard
        id="endpoint-alerts"
        title={t("endpoints.detail.alertRule")}
        description={t("endpoints.detail.alertRuleHint")}
        dirty={threshold !== endpoint.alert.threshold_failures}
        saving={update.isPending}
        error={formError}
        onSubmit={() => save({ alert: { threshold_failures: threshold } })}
      >
        <Input
          label={t("endpoints.detail.threshold")}
          type="number"
          min={1}
          max={100}
          value={threshold}
          onChange={(e) => setThreshold(Math.round(Number(e.target.value) || 0))}
          error={fieldError("threshold")}
          hint={t("endpoints.detail.thresholdHint")}
          className="max-w-40 tabular-nums"
        />
      </FormCard>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("endpoints.detail.channels")}</CardTitle>
            <p className="mt-0.5 text-xs text-muted">{t("endpoints.detail.channelsHint")}</p>
          </div>
          <Link href="/alerts" className="glass inline-flex h-8 items-center gap-1.5 rounded-lg px-3 text-xs font-medium text-fg transition hover:bg-hover">
            <Plus className="size-3.5" />
            {t("alerts.new")}
          </Link>
        </CardHeader>
        <CardBody>
          {applying.length === 0 ? (
            <div className="flex items-center gap-3 text-sm text-muted">
              <Bell className="size-4 text-subtle" />
              {t("endpoints.detail.noChannels")}
            </div>
          ) : (
            <ul className="flex flex-col divide-y divide-line">
              {applying.map((c) => (
                <li key={c.id} className="flex items-center gap-3 py-2.5 text-sm">
                  <span className="text-accent">{channelIcon[c.type]}</span>
                  <span className="font-medium text-fg">{t(`alerts.types.${c.type}`)}</span>
                  <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted">{channelTarget(c)}</span>
                  <Badge tone={c.endpoint_id ? "info" : "neutral"}>{c.endpoint_id ? t("alerts.scope.this") : t("alerts.scope.all")}</Badge>
                  {!c.enabled && <Badge tone="warning">{t("alerts.disabled")}</Badge>}
                </li>
              ))}
            </ul>
          )}
        </CardBody>
      </Card>
    </div>
  );
}
