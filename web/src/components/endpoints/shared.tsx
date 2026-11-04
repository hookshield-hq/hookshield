"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { Fingerprint, KeyRound, Network, ShieldAlert } from "lucide-react";
import type { Endpoint, EndpointInput, EndpointResponse } from "@/lib/queries";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CodeBlock } from "@/components/ui/code";
import { CopyButton } from "@/components/ui/copy-button";
import { Textarea } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { GlassModal } from "@/components/ui/overlay";
import { ChoiceCards } from "@/components/ui/segmented";

export type AuthMode = Endpoint["auth"]["mode"];

const statusTone: Record<Endpoint["status"], Tone> = { active: "success", paused: "warning", debug: "info" };

export function EndpointStatusBadge({ status }: { status: Endpoint["status"] }) {
  const t = useTranslations("endpoints.status");
  return (
    <Badge tone={statusTone[status]}>
      <span className="size-1.5 rounded-full bg-current" />
      {t(status)}
    </Badge>
  );
}

/** A value shown in monospace with a copy button (ingest URL, slug, token). */
export function CopyField({ value, label, className }: { value: string; label?: string; className?: string }) {
  return (
    <div className={`flex min-w-0 items-center gap-1 rounded-lg border border-line bg-surface-strong/70 py-1 pr-1 pl-3 ${className ?? ""}`}>
      <code className="min-w-0 flex-1 truncate font-mono text-xs text-fg" title={value}>
        {value}
      </code>
      <CopyButton value={value} label={label} />
    </div>
  );
}

/** curl command that sends a sample webhook with the endpoint's authentication. */
export function curlExample(endpoint: Endpoint, secret?: string): string {
  const lines = [`curl -X POST '${endpoint.ingest_url}'`, `  -H 'Content-Type: application/json'`];
  if (endpoint.auth.mode === "token") lines.push(`  -H '${endpoint.auth.header}: ${secret ?? "<token>"}'`);
  if (endpoint.auth.mode === "hmac")
    lines.push(`  -H '${endpoint.auth.hmac_header ?? "X-HookShield-Signature"}: sha256=<hex HMAC-SHA256 of the body>'`);
  lines.push(`  -d '{"type":"order.created","id":"ord_123"}'`);
  return lines.join(" \\\n");
}

// ── authentication fields (create drawer and security tab) ──

export type AuthDraft = { mode: AuthMode; header: string; hmacHeader: string; hmacSecret: string; ips: string };

export function authDraftFrom(e?: Endpoint): AuthDraft {
  return {
    mode: e?.auth.mode ?? "token",
    header: e?.auth.header ?? "X-HookShield-Token",
    hmacHeader: e?.auth.hmac_header ?? "X-HookShield-Signature",
    hmacSecret: "",
    ips: (e?.auth.ip_allowlist ?? []).join("\n"),
  };
}

export function authInput(d: AuthDraft): NonNullable<EndpointInput["auth"]> {
  if (d.mode === "token") return { mode: "token", header: d.header.trim() || undefined };
  if (d.mode === "hmac") return { mode: "hmac", hmac_header: d.hmacHeader.trim() || undefined, hmac_secret: d.hmacSecret || undefined };
  return {
    mode: "ip_allowlist",
    ip_allowlist: d.ips
      .split(/[\s,]+/)
      .map((s) => s.trim())
      .filter(Boolean),
  };
}

export function AuthFields({ draft, onChange, fieldError }: { draft: AuthDraft; onChange: (d: AuthDraft) => void; fieldError?: (field: string) => string | undefined }) {
  const t = useTranslations("endpoints.auth");
  const set = (patch: Partial<AuthDraft>) => onChange({ ...draft, ...patch });
  return (
    <div className="flex flex-col gap-4">
      <ChoiceCards
        label={t("mode")}
        value={draft.mode}
        onChange={(mode) => set({ mode })}
        options={[
          { value: "token", label: t("token"), description: t("tokenHint"), icon: <KeyRound className="size-4 text-accent" /> },
          { value: "hmac", label: t("hmac"), description: t("hmacHint"), icon: <Fingerprint className="size-4 text-accent" /> },
          { value: "ip_allowlist", label: t("ip"), description: t("ipHint"), icon: <Network className="size-4 text-accent" /> },
        ]}
      />
      {draft.mode === "token" && (
        <Input label={t("header")} value={draft.header} onChange={(e) => set({ header: e.target.value })} hint={t("headerHint")} error={fieldError?.("header")} className="font-mono" />
      )}
      {draft.mode === "hmac" && (
        <>
          <Input label={t("hmacHeader")} value={draft.hmacHeader} onChange={(e) => set({ hmacHeader: e.target.value })} error={fieldError?.("hmac_header")} className="font-mono" />
          <Input
            label={t("hmacSecret")}
            type="password"
            autoComplete="off"
            value={draft.hmacSecret}
            onChange={(e) => set({ hmacSecret: e.target.value })}
            hint={t("hmacSecretHint")}
            error={fieldError?.("hmac_secret")}
            className="font-mono"
          />
        </>
      )}
      {draft.mode === "ip_allowlist" && (
        <Textarea label={t("ips")} mono value={draft.ips} onChange={(e) => set({ ips: e.target.value })} hint={t("ipsHint")} error={fieldError?.("ip_allowlist")} rows={4} />
      )}
    </div>
  );
}

// ── secrets shown once ──

/** Shows a freshly generated token or HMAC secret — the only time it is ever visible. */
export function SecretReveal({ response, title, onClose, onTest }: { response: EndpointResponse | null; title: string; onClose: () => void; onTest?: () => void }) {
  const t = useTranslations("endpoints.reveal");
  const common = useTranslations("common");
  const [acknowledged, setAcknowledged] = useState(false);
  const e = response?.endpoint;
  const token = response?.secrets?.token;
  const hmac = response?.secrets?.hmac_secret;
  const close = () => {
    setAcknowledged(false);
    onClose();
  };
  return (
    <GlassModal
      open={!!response}
      onClose={close}
      dismissible={!(token || hmac) || acknowledged}
      title={title}
      footer={
        <>
          {onTest && (
            <Button variant="secondary" onClick={onTest} disabled={!!(token || hmac) && !acknowledged}>
              {t("sendTest")}
            </Button>
          )}
          <Button onClick={close} disabled={!!(token || hmac) && !acknowledged}>
            {common("done")}
          </Button>
        </>
      }
    >
      {e && (
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted">{t("ingestUrl")}</span>
            <CopyField value={e.ingest_url} />
          </div>
          {(token || hmac) && (
            <>
              <div className="flex items-start gap-2 rounded-lg bg-warning-bg p-3 text-xs text-warning">
                <ShieldAlert className="mt-0.5 size-4 shrink-0" />
                <p>{t("once")}</p>
              </div>
              <div className="flex flex-col gap-1.5">
                <span className="text-xs font-medium text-muted">{token ? t("token", { header: e.auth.header }) : t("hmacSecret")}</span>
                <CopyField value={(token ?? hmac)!} />
              </div>
              <label className="flex items-center gap-2 text-xs text-muted">
                <input type="checkbox" checked={acknowledged} onChange={(ev) => setAcknowledged(ev.target.checked)} className="size-4 accent-[var(--hs-accent)]" />
                {t("acknowledge")}
              </label>
            </>
          )}
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted">{t("tryIt")}</span>
            <CodeBlock value={curlExample(e, token)} />
          </div>
        </div>
      )}
    </GlassModal>
  );
}

/** Field-level message for validation codes of the endpoint API. */
export function endpointFieldOf(code: string): string | undefined {
  const map: Record<string, string> = {
    "endpoint.invalid_name": "name",
    "endpoint.invalid_description": "description",
    "endpoint.invalid_slug": "slug",
    "endpoint.slug_taken": "slug",
    "endpoint.invalid_target_url": "target_url",
    "endpoint.private_target": "target_url",
    "endpoint.invalid_header": "header",
    "endpoint.invalid_hmac_header": "hmac_header",
    "endpoint.invalid_hmac_secret": "hmac_secret",
    "endpoint.invalid_ip_allowlist": "ip_allowlist",
    "endpoint.ip_allowlist_required": "ip_allowlist",
    "endpoint.too_many_outbound_headers": "outbound_headers",
    "endpoint.invalid_outbound_header": "outbound_headers",
    "endpoint.invalid_outbound_header_value": "outbound_headers",
    "retry.invalid_strategy": "retry",
    "retry.invalid_max_retries": "max_retries",
    "retry.invalid_initial_interval": "initial_interval",
    "retry.invalid_multiplier": "multiplier",
    "retry.invalid_max_delay": "max_delay",
    "retry.invalid_custom_schedule": "custom_schedule",
    "alert.invalid_threshold": "threshold",
    "alert.invalid_webhook_url": "alert_webhook",
    "alert.private_target": "alert_webhook",
  };
  return map[code];
}
