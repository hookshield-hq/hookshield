"use client";

import { useMemo, useState, type FormEvent } from "react";
import { useLocale, useTranslations } from "next-intl";
import { Bell, Plus, Trash2 } from "lucide-react";
import { useAlertChannels, useCreateAlertChannel, useDeleteAlertChannel, useEndpoints, useUpdateAlertChannel, type AlertChannel, type ChannelTarget } from "@/lib/queries";
import { formatRelative } from "@/lib/format";
import { useErrorMessage, useFieldErrors } from "@/lib/use-error-message";
import { FormError } from "@/components/auth/fields";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
import { channelIcon, channelTarget } from "@/components/endpoints/endpoint-sections";

type Type = AlertChannel["type"];
const TYPES: Type[] = ["email", "slack", "webhook", "telegram"];

function CreateChannel({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslations("alerts");
  const common = useTranslations("common");
  const ex = useTranslations("examples");
  const toast = useToast();
  const endpoints = useEndpoints();
  const create = useCreateAlertChannel();
  const [type, setType] = useState<Type>("email");
  const [target, setTarget] = useState<ChannelTarget>({});
  const [scope, setScope] = useState("");
  const { fieldError, formError } = useFieldErrors(create.error, (c) => (c === "alert.invalid_target" || c === "alert.private_target" ? "target" : undefined));
  const set = (patch: ChannelTarget) => setTarget({ ...target, ...patch });

  function submit(e: FormEvent) {
    e.preventDefault();
    const body: ChannelTarget =
      type === "email" ? { to: target.to?.trim() ?? "" } : type === "slack" ? { webhook_url: target.webhook_url?.trim() } : type === "webhook" ? { url: target.url?.trim() } : { bot_token: target.bot_token?.trim(), chat_id: target.chat_id?.trim() };
    create.mutate({ type, target: body, endpoint_id: scope || null }, { onSuccess: () => (toast.success(t("created")), onClose()) });
  }

  return (
    <GlassModal
      open={open}
      onClose={onClose}
      title={t("new")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {common("cancel")}
          </Button>
          <Button type="submit" form="channel-form" loading={create.isPending}>
            {common("create")}
          </Button>
        </>
      }
    >
      <form id="channel-form" onSubmit={submit} className="flex flex-col gap-4" noValidate>
        <ChoiceCards
          label={t("type")}
          columns={2}
          value={type}
          onChange={(v) => (setType(v), setTarget({}), create.reset())}
          options={TYPES.map((v) => ({ value: v, label: t(`types.${v}`), description: t(`typeHints.${v}`), icon: <span className="text-accent">{channelIcon[v]}</span> }))}
        />
        {type === "email" && <Input label={t("fields.email")} type="email" placeholder={ex("email")} value={target.to ?? ""} onChange={(e) => set({ to: e.target.value })} hint={t("fields.emailHint")} error={fieldError("target")} />}
        {type === "slack" && <Input label={t("fields.slack")} type="url" placeholder={ex("slackUrl")} value={target.webhook_url ?? ""} onChange={(e) => set({ webhook_url: e.target.value })} error={fieldError("target")} className="font-mono" />}
        {type === "webhook" && <Input label={t("fields.url")} type="url" placeholder={ex("alertUrl")} value={target.url ?? ""} onChange={(e) => set({ url: e.target.value })} hint={t("fields.urlHint")} error={fieldError("target")} className="font-mono" />}
        {type === "telegram" && (
          <>
            <Input label={t("fields.botToken")} type="password" autoComplete="off" placeholder={ex("botToken")} value={target.bot_token ?? ""} onChange={(e) => set({ bot_token: e.target.value })} error={fieldError("target")} className="font-mono" />
            <Input label={t("fields.chatId")} placeholder={ex("chatId")} value={target.chat_id ?? ""} onChange={(e) => set({ chat_id: e.target.value })} hint={t("fields.chatIdHint")} className="font-mono" />
          </>
        )}
        <Select label={t("scopeLabel")} value={scope} onChange={(e) => setScope(e.target.value)}>
          <option value="">{t("scope.all")}</option>
          {(endpoints.data ?? []).map((e) => (
            <option key={e.id} value={e.id}>
              {t("scope.only", { name: e.name })}
            </option>
          ))}
        </Select>
        <FormError message={formError} />
      </form>
    </GlassModal>
  );
}

export function AlertsPage() {
  const t = useTranslations("alerts");
  const common = useTranslations("common");
  const locale = useLocale();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const channels = useAlertChannels();
  const endpoints = useEndpoints();
  const update = useUpdateAlertChannel();
  const del = useDeleteAlertChannel();
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<AlertChannel | null>(null);
  const names = useMemo(() => new Map((endpoints.data ?? []).map((e) => [e.id, e.name])), [endpoints.data]);
  const newButton = (
    <Button onClick={() => setCreating(true)}>
      <Plus className="size-4" />
      {t("new")}
    </Button>
  );

  return (
    <div className="mx-auto max-w-6xl">
      <PageHeader title={t("title")} description={t("subtitle")} actions={channels.data?.length ? newButton : undefined} />
      <Card>
        {channels.isPending ? (
          <div className="p-5">
            <Skeleton className="h-24 w-full" />
          </div>
        ) : channels.isError ? (
          <EmptyState title={common("errorTitle")} body={errorMessage(channels.error)} action={<Button onClick={() => channels.refetch()}>{common("retry")}</Button>} />
        ) : channels.data.length === 0 ? (
          <EmptyState icon={<Bell className="size-6" />} title={t("emptyTitle")} body={t("emptyBody")} action={newButton} />
        ) : (
          <Table>
            <THead>
              <TR className="hover:bg-transparent">
                <TH>{t("type")}</TH>
                <TH>{t("target")}</TH>
                <TH>{t("scopeLabel")}</TH>
                <TH>{t("createdAt")}</TH>
                <TH>{t("enabled")}</TH>
                <TH className="text-right">
                  <span className="sr-only">{t("actions")}</span>
                </TH>
              </TR>
            </THead>
            <TBody>
              {channels.data.map((c) => (
                <TR key={c.id}>
                  <TD>
                    <span className="inline-flex items-center gap-2 font-medium">
                      <span className="text-accent">{channelIcon[c.type]}</span>
                      {t(`types.${c.type}`)}
                    </span>
                  </TD>
                  <TD className="max-w-72 truncate font-mono text-xs text-muted">{c.type === "email" && !c.target.to ? t("accountEmail") : channelTarget(c)}</TD>
                  <TD className="text-xs text-muted">{c.endpoint_id ? (names.get(c.endpoint_id) ?? "—") : t("scope.all")}</TD>
                  <TD className="text-xs text-muted">{formatRelative(locale, c.created_at)}</TD>
                  <TD>
                    <button
                      type="button"
                      role="switch"
                      aria-checked={c.enabled}
                      aria-label={t("toggle", { type: t(`types.${c.type}`) })}
                      onClick={() => update.mutate({ id: c.id, body: { enabled: !c.enabled } }, { onError: (err) => toast.error(errorMessage(err)) })}
                      className={`relative inline-flex h-5 w-9 items-center rounded-full transition ${c.enabled ? "bg-accent-gradient" : "bg-line"}`}
                    >
                      <span className={`inline-block size-4 rounded-full bg-white shadow transition-transform ${c.enabled ? "translate-x-4.5" : "translate-x-0.5"}`} />
                    </button>
                  </TD>
                  <TD className="text-right">
                    <Button variant="ghost" size="icon" aria-label={t("deleteTitle")} title={common("delete")} onClick={() => setDeleting(c)}>
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
      {creating && <CreateChannel open={creating} onClose={() => setCreating(false)} />}
      <ConfirmDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={t("deleteTitle")}
        body={t("deleteBody")}
        confirmLabel={common("delete")}
        loading={del.isPending}
        onConfirm={() =>
          deleting &&
          del.mutate(deleting.id, {
            onSuccess: () => (setDeleting(null), toast.success(t("deleted"))),
            onError: (err) => (setDeleting(null), toast.error(errorMessage(err))),
          })
        }
      />
    </div>
  );
}
