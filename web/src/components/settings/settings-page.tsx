"use client";

import { useState, type FormEvent } from "react";
import { useLocale, useTranslations } from "next-intl";
import { useTheme } from "next-themes";
import { BadgeCheck, CreditCard, MailWarning } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { locales, type Locale } from "@/i18n/routing";
import { useMe, useUpdateMe } from "@/lib/auth";
import { useChangePassword, useFeatures, useMetricsSummary, useResendVerification } from "@/lib/queries";
import { formatDateTime } from "@/lib/format";
import { useErrorMessage, useFieldErrors } from "@/lib/use-error-message";
import { FormError, PasswordInput } from "@/components/auth/fields";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Segmented } from "@/components/ui/segmented";
import { useToast } from "@/components/ui/toast";
import { themeIcons, themes, useSwitchLocale, useSwitchTheme, type Theme } from "@/components/shell/preferences";

function Section({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{title}</CardTitle>
          {description && <p className="mt-0.5 text-xs text-muted">{description}</p>}
        </div>
      </CardHeader>
      <CardBody>{children}</CardBody>
    </Card>
  );
}

function Profile() {
  const t = useTranslations("settings");
  const locale = useLocale();
  const toast = useToast();
  const errorMessage = useErrorMessage();
  const me = useMe();
  const update = useUpdateMe();
  const resend = useResendVerification();
  const user = me.data!;
  const [name, setName] = useState(user.name ?? "");
  const { fieldError, formError } = useFieldErrors(update.error, (c) => (c === "auth.invalid_name" ? "name" : undefined));
  return (
    <Section title={t("profile")} description={t("profileHint")}>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          update.mutate({ name: name.trim() }, { onSuccess: () => toast.success(t("profileSaved")) });
        }}
      >
        <Input label={t("name")} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} error={fieldError("name")} />
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-muted">{t("email")}</span>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-fg">{user.email}</span>
            {user.email_verified ? (
              <Badge tone="success">
                <BadgeCheck className="size-3" />
                {t("verified")}
              </Badge>
            ) : (
              <>
                <Badge tone="warning">
                  <MailWarning className="size-3" />
                  {t("unverified")}
                </Badge>
                <Button
                  variant="ghost"
                  size="sm"
                  loading={resend.isPending}
                  onClick={() => resend.mutate(undefined, { onSuccess: () => toast.success(t("verificationSent")), onError: (err) => toast.error(errorMessage(err)) })}
                >
                  {t("resend")}
                </Button>
              </>
            )}
          </div>
        </div>
        <p className="text-xs text-subtle">{t("memberSince", { date: formatDateTime(locale, user.created_at) })}</p>
        <FormError message={formError} />
        <div className="flex justify-end">
          <Button type="submit" disabled={name.trim() === (user.name ?? "")} loading={update.isPending}>
            {t("saveProfile")}
          </Button>
        </div>
      </form>
    </Section>
  );
}

function Preferences() {
  const t = useTranslations("settings");
  const shell = useTranslations("shell");
  const locale = useLocale() as Locale;
  const { theme } = useTheme();
  const switchLocale = useSwitchLocale();
  const switchTheme = useSwitchTheme();
  return (
    <Section title={t("preferences")} description={t("preferencesHint")}>
      <div className="flex flex-col gap-5">
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-muted">{shell("language.label")}</span>
          <Segmented label={shell("language.label")} value={locale} onChange={(l: Locale) => switchLocale(l)} options={locales.map((l) => ({ value: l, label: shell(`language.${l}`) }))} />
          <p className="text-xs text-subtle">{t("languageHint")}</p>
        </div>
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-muted">{shell("theme.label")}</span>
          <Segmented
            label={shell("theme.label")}
            value={(theme as Theme) ?? "dark"}
            onChange={(v: Theme) => switchTheme(v)}
            options={themes.map((th) => {
              const Icon = themeIcons[th];
              return { value: th, label: shell(`theme.${th}`), icon: <Icon className="size-3.5" /> };
            })}
          />
        </div>
      </div>
    </Section>
  );
}

function Password() {
  const t = useTranslations("settings");
  const toast = useToast();
  const me = useMe();
  const change = useChangePassword();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const { fieldError, formError } = useFieldErrors(change.error, (c) => (c === "auth.wrong_password" ? "current" : c.startsWith("password.") ? "next" : undefined));
  if (!me.data?.has_password)
    return (
      <Section title={t("password")} description={t("passwordHint")}>
        <p className="text-sm text-muted">
          {t.rich("noPassword", {
            link: (c) => (
              <Link href="/forgot-password" className="text-accent underline underline-offset-2 hover:decoration-2">
                {c}
              </Link>
            ),
          })}
        </p>
      </Section>
    );
  return (
    <Section title={t("password")} description={t("passwordHint")}>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e: FormEvent) => {
          e.preventDefault();
          change.mutate(
            { current_password: current, new_password: next },
            {
              onSuccess: () => {
                setCurrent("");
                setNext("");
                toast.success(t("passwordChanged"));
              },
            },
          );
        }}
      >
        <PasswordInput label={t("currentPassword")} autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} error={fieldError("current")} />
        <PasswordInput label={t("newPassword")} autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} hint={t("newPasswordHint")} error={fieldError("next")} />
        <FormError message={formError} />
        <div className="flex justify-end">
          <Button type="submit" disabled={!current || next.length < 10} loading={change.isPending}>
            {t("changePassword")}
          </Button>
        </div>
      </form>
    </Section>
  );
}

function Plan() {
  const t = useTranslations("settings");
  const me = useMe();
  const usage = useMetricsSummary("30d");
  const features = useFeatures();
  const plan = me.data?.plan_type ?? "developer";
  return (
    <Section title={t("plan")} description={t("planHint")}>
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <span className="bg-accent-gradient flex size-10 items-center justify-center rounded-lg text-white">
            <CreditCard className="size-5" />
          </span>
          <div>
            <div className="text-sm font-semibold text-fg">{t("planName", { plan: t.has(`plans.${plan}`) ? t(`plans.${plan}`) : plan })}</div>
            <div className="text-xs text-muted">{usage.data ? t("usage", { count: usage.data.total_received }) : "—"}</div>
          </div>
        </div>
        {features.data?.billing ? (
          <Link href="/billing" className="glass inline-flex h-9 items-center gap-2 rounded-lg px-4 text-sm font-medium text-fg transition hover:bg-hover">
            {t("managePlan")}
          </Link>
        ) : (
          <p className="max-w-sm text-xs text-subtle">{t("billingSoon")}</p>
        )}
      </div>
    </Section>
  );
}

export function SettingsPage() {
  const t = useTranslations("settings");
  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title={t("title")} description={t("subtitle")} />
      <div className="flex flex-col gap-6">
        <Profile />
        <Preferences />
        <Password />
        <Plan />
      </div>
    </div>
  );
}
