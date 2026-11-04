"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useLocale, useTranslations } from "next-intl";
import { Eye, KeyRound, Plus, ShieldAlert } from "lucide-react";
import { useApiKeys, useCreateApiKey, useRevokeApiKey, type APIKey } from "@/lib/queries";
import { formatDateTime, formatRelative } from "@/lib/format";
import { useErrorMessage, useFieldErrors } from "@/lib/use-error-message";
import { FormError } from "@/components/auth/fields";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock } from "@/components/ui/code";
import { ConfirmDialog } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty-state";
import { Select } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { GlassModal } from "@/components/ui/overlay";
import { PageHeader } from "@/components/ui/page-header";
import { ChoiceCards } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";
import { CopyField } from "@/components/endpoints/shared";
import { apiBaseURL } from "@/lib/api/config";

const scopeTone: Record<APIKey["scope"], Tone> = { read_only: "neutral", ingest: "info", admin: "warning" };

function keyState(k: APIKey): "active" | "revoked" | "expired" {
  if (k.revoked_at) return "revoked";
  if (k.expires_at && new Date(k.expires_at) < new Date()) return "expired";
  return "active";
}

function CreateKey({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslations("apiKeys");
  const common = useTranslations("common");
  const create = useCreateApiKey();
  const [name, setName] = useState("");
  const [scope, setScope] = useState<APIKey["scope"]>("read_only");
  const [expiry, setExpiry] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const { fieldError, formError } = useFieldErrors(create.error, (c) => (c === "api_key.invalid_name" ? "name" : undefined));

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate({ name: name.trim(), scope, expires_in_days: expiry ? Number(expiry) : undefined }, { onSuccess: (r) => setSecret(r.key) });
  }
  // The key is masked for good as soon as the window closes.
  const close = () => {
    setSecret(null);
    setCopied(false);
    onClose();
  };

  return (
    <GlassModal
      open={open}
      onClose={close}
      dismissible={!secret || copied}
      title={secret ? t("createdTitle") : t("new")}
      footer={
        secret ? (
          <Button onClick={close} disabled={!copied}>
            {common("done")}
          </Button>
        ) : (
          <>
            <Button variant="ghost" onClick={close}>
              {common("cancel")}
            </Button>
            <Button type="submit" form="api-key-form" loading={create.isPending} disabled={!name.trim()}>
              {t("create")}
            </Button>
          </>
        )
      }
    >
      {secret ? (
        <div className="flex flex-col gap-4">
          <div className="flex items-start gap-2 rounded-lg bg-warning-bg p-3 text-xs text-warning">
            <ShieldAlert className="mt-0.5 size-4 shrink-0" />
            <p>{t("once")}</p>
          </div>
          <div onCopy={() => setCopied(true)} onClickCapture={() => setCopied(true)}>
            <CopyField value={secret} />
          </div>
          <p className="text-xs text-subtle">{copied ? t("copiedHint") : t("copyFirst")}</p>
        </div>
      ) : (
        <form id="api-key-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
          <Input label={t("name")} placeholder={t("namePlaceholder")} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} error={fieldError("name")} autoFocus />
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium text-muted">{t("scope")}</span>
            <ChoiceCards
              label={t("scope")}
              columns={3}
              value={scope}
              onChange={setScope}
              options={(["read_only", "ingest", "admin"] as const).map((s) => ({ value: s, label: t(`scopes.${s}`), description: t(`scopes.${s}Hint`) }))}
            />
          </div>
          <Select label={t("expires")} value={expiry} onChange={(e) => setExpiry(e.target.value)}>
            <option value="">{t("noExpiry")}</option>
            {[30, 90, 365].map((d) => (
              <option key={d} value={d}>
                {t("days", { n: d })}
              </option>
            ))}
          </Select>
          <FormError message={formError} />
        </form>
      )}
    </GlassModal>
  );
}

export function ApiKeysPage() {
  const t = useTranslations("apiKeys");
  const common = useTranslations("common");
  const locale = useLocale();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const keys = useApiKeys();
  const revoke = useRevokeApiKey();
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<APIKey | null>(null);
  const [base, setBase] = useState("https://api.example.com");
  useEffect(() => {
    apiBaseURL().then(setBase, () => undefined);
  }, []);

  const newButton = (
    <Button onClick={() => setCreating(true)}>
      <Plus className="size-4" />
      {t("new")}
    </Button>
  );

  return (
    <div className="mx-auto max-w-6xl">
      <PageHeader title={t("title")} description={t("subtitle")} actions={keys.data?.length ? newButton : undefined} />
      <Card className="mb-6">
        {keys.isPending ? (
          <div className="p-5">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : keys.isError ? (
          <EmptyState title={common("errorTitle")} body={errorMessage(keys.error)} action={<Button onClick={() => keys.refetch()}>{common("retry")}</Button>} />
        ) : keys.data.length === 0 ? (
          <EmptyState icon={<KeyRound className="size-6" />} title={t("emptyTitle")} body={t("emptyBody")} action={newButton} />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <TH>{t("name")}</TH>
                <TH>{t("key")}</TH>
                <TH>{t("scope")}</TH>
                <TH>{t("created")}</TH>
                <TH>{t("lastUsed")}</TH>
                <TH>{t("status")}</TH>
                <TH className="text-right">
                  <span className="sr-only">{t("actions")}</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {keys.data.map((k) => {
                const state = keyState(k);
                return (
                  <TR key={k.id} className={state !== "active" ? "opacity-60" : undefined}>
                    <TD className="font-medium">{k.name}</TD>
                    <TD className="font-mono text-xs text-muted">{k.prefix}…</TD>
                    <TD>
                      <Badge tone={scopeTone[k.scope]}>{t(`scopes.${k.scope}`)}</Badge>
                    </TD>
                    <TD className="text-xs text-muted" title={formatDateTime(locale, k.created_at)}>
                      {formatRelative(locale, k.created_at)}
                    </TD>
                    <TD className="text-xs text-muted">{k.last_used_at ? formatRelative(locale, k.last_used_at) : common("never")}</TD>
                    <TD>
                      <Badge tone={state === "active" ? "success" : "danger"}>{t(`states.${state}`)}</Badge>
                      {state === "active" && k.expires_at && <span className="ml-2 text-xs text-subtle">{t("expiresIn", { when: formatRelative(locale, k.expires_at) })}</span>}
                    </TD>
                    <TD className="text-right">
                      {state === "active" && (
                        <Button variant="ghost" size="sm" onClick={() => setRevoking(k)}>
                          {t("revoke")}
                        </Button>
                      )}
                    </TD>
                  </TR>
                );
              })}
            </TBody>
          </Table>
        )}
      </Card>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("usage")}</CardTitle>
            <p className="mt-0.5 text-xs text-muted">{t("usageHint")}</p>
          </div>
          <Eye className="size-4 text-subtle" />
        </CardHeader>
        <CardBody>
          <CodeBlock value={`curl ${base}/v1/logs?limit=10 \\\n  -H 'Authorization: Bearer hs_live_…'`} />
        </CardBody>
      </Card>
      <CreateKey open={creating} onClose={() => setCreating(false)} />
      <ConfirmDialog
        open={!!revoking}
        onClose={() => setRevoking(null)}
        title={t("revokeTitle", { name: revoking?.name ?? "" })}
        body={t("revokeBody")}
        confirmLabel={t("revoke")}
        loading={revoke.isPending}
        onConfirm={() =>
          revoking &&
          revoke.mutate(revoking.id, {
            onSuccess: () => (setRevoking(null), toast.success(t("revoked"))),
            onError: (err) => (setRevoking(null), toast.error(errorMessage(err))),
          })
        }
      />
    </div>
  );
}
