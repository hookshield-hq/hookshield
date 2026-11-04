"use client";

import { useState, type FormEvent } from "react";
import { useTranslations } from "next-intl";
import { ChevronRight } from "lucide-react";
import { useRouter } from "@/i18n/navigation";
import { useCreateEndpoint, useTestEndpoint, type EndpointResponse } from "@/lib/queries";
import { useFieldErrors } from "@/lib/use-error-message";
import { useResetOnHide } from "@/lib/use-reset-on-hide";
import { FormError } from "@/components/auth/fields";
import { Button } from "@/components/ui/button";
import { Fieldset, Textarea } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Drawer } from "@/components/ui/overlay";
import { useToast } from "@/components/ui/toast";
import { AuthFields, authDraftFrom, authInput, endpointFieldOf, SecretReveal, type AuthDraft } from "./shared";

const empty = () => ({ name: "", description: "", target: "", slug: "", auth: authDraftFrom() });

/** "New endpoint" drawer, then the one-time token/secret reveal. */
export function CreateEndpoint({ open, onClose }: { open: boolean; onClose: () => void }) {
  const t = useTranslations("endpoints.create");
  const common = useTranslations("common");
  const ex = useTranslations("examples");
  const router = useRouter();
  const toast = useToast();
  const create = useCreateEndpoint();
  const test = useTestEndpoint();
  const [form, setForm] = useState(empty);
  const [advanced, setAdvanced] = useState(false);
  const [created, setCreated] = useState<EndpointResponse | null>(null);
  const { fieldError, formError } = useFieldErrors(create.error, endpointFieldOf);

  const reset = () => {
    setForm(empty());
    setAdvanced(false);
    create.reset();
  };
  useResetOnHide(() => {
    reset();
    setCreated(null);
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate(
      {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        target_url: form.target.trim(),
        slug: form.slug.trim() || undefined,
        auth: authInput(form.auth),
      },
      {
        onSuccess: (res) => {
          onClose();
          reset();
          setCreated(res);
        },
      },
    );
  }

  function openCreated() {
    const id = created?.endpoint.id;
    setCreated(null);
    if (id) router.push(`/endpoints/${id}`);
  }

  function sendTest() {
    const id = created?.endpoint.id;
    if (!id) return;
    test.mutate(
      { id },
      {
        onSuccess: (r) => {
          setCreated(null);
          toast.success(t("testSent"));
          router.push(`/logs?id=${r.id}`);
        },
      },
    );
  }

  return (
    <>
      <Drawer
        open={open}
        onClose={() => {
          onClose();
          create.reset();
        }}
        title={t("title")}
        footer={
          <>
            <Button variant="ghost" onClick={onClose}>
              {common("cancel")}
            </Button>
            <Button type="submit" form="create-endpoint" loading={create.isPending}>
              {t("submit")}
            </Button>
          </>
        }
      >
        <form id="create-endpoint" onSubmit={submit} className="flex flex-col gap-8" noValidate>
          <Fieldset legend={t("basics")} description={t("basicsHint")}>
            <Input label={t("name")} placeholder={t("namePlaceholder")} required maxLength={100} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} error={fieldError("name")} autoFocus />
            <Input
              label={t("target")}
              placeholder={t("targetPlaceholder")}
              type="url"
              required
              value={form.target}
              onChange={(e) => setForm({ ...form, target: e.target.value })}
              hint={t("targetHint")}
              error={fieldError("target_url")}
              className="font-mono"
            />
            <Textarea label={t("description")} maxLength={1000} rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} error={fieldError("description")} />
          </Fieldset>
          <Fieldset legend={t("security")} description={t("securityHint")}>
            <AuthFields draft={form.auth} onChange={(auth: AuthDraft) => setForm({ ...form, auth })} fieldError={fieldError} />
          </Fieldset>
          <div>
            <button type="button" aria-expanded={advanced} onClick={() => setAdvanced(!advanced)} className="inline-flex items-center gap-1 text-sm font-medium text-muted transition hover:text-fg">
              <ChevronRight className={`size-4 transition ${advanced ? "rotate-90" : ""}`} />
              {t("advanced")}
            </button>
            {advanced && (
              <div className="mt-4">
                <Input label={t("slug")} placeholder={ex("slug")} value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value.toLowerCase() })} hint={t("slugHint")} error={fieldError("slug")} className="font-mono" />
              </div>
            )}
          </div>
          <FormError message={formError} />
        </form>
      </Drawer>
      <SecretReveal
        response={created}
        title={t("created")}
        onClose={openCreated}
        onTest={created?.endpoint.status !== "paused" ? sendTest : undefined}
      />
    </>
  );
}
