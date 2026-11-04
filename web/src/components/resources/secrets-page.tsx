"use client";

import { useState, type FormEvent } from "react";
import { useLocale, useTranslations } from "next-intl";
import { Lock, Pencil, Plus, Trash2 } from "lucide-react";
import { useDeleteSecret, usePutSecret, useSecrets, type Secret } from "@/lib/queries";
import { formatRelative } from "@/lib/format";
import { useErrorMessage, useFieldErrors } from "@/lib/use-error-message";
import { FormError } from "@/components/auth/fields";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ui/confirm";
import { CopyButton } from "@/components/ui/copy-button";
import { EmptyState } from "@/components/ui/empty-state";
import { Textarea } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { GlassModal } from "@/components/ui/overlay";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TBody, TD, TH, THead, TR } from "@/components/ui/table";
import { useToast } from "@/components/ui/toast";

const NAME_RE = /^[A-Z][A-Z0-9_]{0,63}$/;

function SecretForm({ editing, open, onClose }: { editing: Secret | null; open: boolean; onClose: () => void }) {
  const t = useTranslations("secrets");
  const common = useTranslations("common");
  const ex = useTranslations("examples");
  const toast = useToast();
  const put = usePutSecret();
  const [name, setName] = useState(editing?.name ?? "");
  const [value, setValue] = useState("");
  const [touched, setTouched] = useState(false);
  const { fieldError, formError } = useFieldErrors(put.error, (c) => (c === "secret.invalid_name" ? "name" : c === "secret.invalid_value" ? "value" : undefined));
  const nameError = touched && !NAME_RE.test(name) ? t("nameRule") : fieldError("name");

  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!NAME_RE.test(name) || !value) return;
    put.mutate({ name, value }, { onSuccess: () => (toast.success(editing ? t("replaced") : t("created")), onClose()) });
  }

  return (
    <GlassModal
      open={open}
      onClose={onClose}
      title={editing ? t("replaceTitle", { name: editing.name }) : t("new")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {common("cancel")}
          </Button>
          <Button type="submit" form="secret-form" loading={put.isPending} disabled={!name || !value}>
            {editing ? t("replace") : common("create")}
          </Button>
        </>
      }
    >
      <form id="secret-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <Input
          label={t("name")}
          value={name}
          disabled={!!editing}
          autoFocus={!editing}
          placeholder={ex("secretName")}
          onChange={(e) => setName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_"))}
          onBlur={() => setTouched(true)}
          hint={t("nameHint", { ref: `{{secrets.${name || "NAME"}}}` })}
          error={nameError}
          className="font-mono"
        />
        <Textarea label={t("value")} mono rows={3} autoFocus={!!editing} value={value} onChange={(e) => setValue(e.target.value.replace(/[\r\n]/g, ""))} hint={t("valueHint")} error={fieldError("value")} autoComplete="off" />
        <FormError message={formError} />
      </form>
    </GlassModal>
  );
}

export function SecretsPage() {
  const t = useTranslations("secrets");
  const common = useTranslations("common");
  const locale = useLocale();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const secrets = useSecrets();
  const del = useDeleteSecret();
  const [form, setForm] = useState<{ open: boolean; editing: Secret | null }>({ open: false, editing: null });
  const [deleting, setDeleting] = useState<Secret | null>(null);
  const newButton = (
    <Button onClick={() => setForm({ open: true, editing: null })}>
      <Plus className="size-4" />
      {t("new")}
    </Button>
  );

  return (
    <div className="mx-auto max-w-5xl">
      <PageHeader title={t("title")} description={t("subtitle")} actions={secrets.data?.length ? newButton : undefined} />
      <Card>
        {secrets.isPending ? (
          <div className="p-5">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : secrets.isError ? (
          <EmptyState title={common("errorTitle")} body={errorMessage(secrets.error)} action={<Button onClick={() => secrets.refetch()}>{common("retry")}</Button>} />
        ) : secrets.data.length === 0 ? (
          <EmptyState icon={<Lock className="size-6" />} title={t("emptyTitle")} body={t("emptyBody", { ref: "{{secrets.NAME}}" })} action={newButton} />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <TH>{t("name")}</TH>
                <TH>{t("reference")}</TH>
                <TH>{t("updated")}</TH>
                <TH className="text-right">
                  <span className="sr-only">{t("actions")}</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {secrets.data.map((s) => (
                <TR key={s.name}>
                  <TD className="font-mono text-xs font-medium">{s.name}</TD>
                  <TD>
                    <div className="flex items-center gap-1">
                      <code className="font-mono text-xs text-accent">{s.reference}</code>
                      <CopyButton value={s.reference} label={t("copyReference")} />
                    </div>
                  </TD>
                  <TD className="text-xs text-muted">{formatRelative(locale, s.updated_at)}</TD>
                  <TD className="text-right">
                    <Button variant="ghost" size="icon" aria-label={t("replaceTitle", { name: s.name })} title={t("replace")} onClick={() => setForm({ open: true, editing: s })}>
                      <Pencil className="size-4" />
                    </Button>
                    <Button variant="ghost" size="icon" aria-label={t("deleteTitle", { name: s.name })} title={common("delete")} onClick={() => setDeleting(s)}>
                      <Trash2 className="size-4" />
                    </Button>
                  </TD>
                </TR>
              ))}
            </TBody>
          </Table>
        )}
      </Card>
      <p className="mt-3 text-xs text-subtle">{t("note")}</p>
      {form.open && <SecretForm open={form.open} editing={form.editing} onClose={() => setForm({ open: false, editing: null })} />}
      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("deleteTitle", { name: deleting?.name ?? "" })}
        body={t("deleteBody")}
        confirmLabel={common("delete")}
        loading={del.isPending}
        onConfirm={() =>
          deleting &&
          del.mutate(deleting.name, {
            onSuccess: () => (setDeleting(null), toast.success(t("deleted"))),
            onError: (err) => (setDeleting(null), toast.error(errorMessage(err))),
          })
        }
      />
    </div>
  );
}
