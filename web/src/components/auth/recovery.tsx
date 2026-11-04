"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "next/navigation";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslations } from "next-intl";
import { CheckCircle2 } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { api } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";
import { useErrorMessage } from "@/lib/use-error-message";
import { useResetOnHide } from "@/lib/use-reset-on-hide";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { AuthHeading, EMAIL_RE, FormError, FormNotice, PasswordInput } from "./fields";

export function ForgotPasswordForm() {
  const t = useTranslations("auth");
  const v = useTranslations("validation");
  const errorMessage = useErrorMessage();
  const [email, setEmail] = useState("");
  const [touched, setTouched] = useState(false);
  const send = useMutation({
    mutationFn: async (address: string) => {
      await (await api()).POST("/v1/auth/forgot-password", { body: { email: address } });
    },
  });
  const emailError = touched && !EMAIL_RE.test(email.trim()) ? v("email") : undefined;
  useResetOnHide(() => {
    setTouched(false);
    send.reset();
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (EMAIL_RE.test(email.trim())) send.mutate(email.trim());
  }

  return (
    <>
      <AuthHeading title={t("forgot.title")} subtitle={t("forgot.subtitle")} />
      {send.isSuccess ? (
        <FormNotice message={t("forgot.sent", { email: email.trim() })} />
      ) : (
        <form onSubmit={submit} noValidate className="flex flex-col gap-4">
          <FormError message={send.error ? errorMessage(send.error) : null} />
          <Input label={t("email")} type="email" autoComplete="email" placeholder={t("emailPlaceholder")} value={email} onChange={(e) => setEmail(e.target.value)} error={emailError} autoFocus />
          <Button type="submit" size="lg" loading={send.isPending} className="w-full justify-center">
            {t("forgot.submit")}
          </Button>
        </form>
      )}
      <p className="mt-6 text-center text-sm">
        <Link href="/login" className="font-medium text-accent underline underline-offset-2 hover:decoration-2">
          {t("forgot.backToLogin")}
        </Link>
      </p>
    </>
  );
}

export function ResetPasswordForm() {
  const t = useTranslations("auth");
  const v = useTranslations("validation");
  const errorMessage = useErrorMessage();
  const token = useSearchParams().get("token") ?? "";
  const [password, setPassword] = useState("");
  const [touched, setTouched] = useState(false);
  const qc = useQueryClient();
  const reset = useMutation({
    mutationFn: async () => {
      await (await api()).POST("/v1/auth/reset-password", { body: { token, password } });
    },
    onSuccess: () => qc.setQueryData(["me"], null),
  });
  const passwordError = touched && password.length < 10 ? v("passwordTooShort") : undefined;
  useResetOnHide(() => {
    setPassword("");
    setTouched(false);
  });

  if (!token) {
    return (
      <>
        <AuthHeading title={t("reset.title")} />
        <FormError message={t("reset.missingToken")} />
        <Link href="/forgot-password" className="mt-6 block text-center text-sm font-medium text-accent hover:underline">
          {t("reset.requestNew")}
        </Link>
      </>
    );
  }

  return (
    <>
      <AuthHeading title={t("reset.title")} subtitle={t("reset.subtitle")} />
      {reset.isSuccess ? (
        <div className="flex flex-col gap-4">
          <FormNotice message={t("reset.done")} />
          <Link href="/login" className="bg-accent-gradient inline-flex h-11 items-center justify-center rounded-lg text-sm font-medium text-white">
            {t("login.submit")}
          </Link>
        </div>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setTouched(true);
            if (password.length >= 10) reset.mutate();
          }}
          noValidate
          className="flex flex-col gap-4"
        >
          <FormError message={reset.error ? errorMessage(reset.error) : null} />
          <PasswordInput label={t("reset.newPassword")} autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} hint={t("register.passwordHint")} error={passwordError} autoFocus />
          <Button type="submit" size="lg" loading={reset.isPending} className="w-full justify-center">
            {t("reset.submit")}
          </Button>
          {reset.error && (
            <Link href="/forgot-password" className="text-center text-sm font-medium text-accent hover:underline">
              {t("reset.requestNew")}
            </Link>
          )}
        </form>
      )}
    </>
  );
}

export function VerifyEmail() {
  const t = useTranslations("auth.verify");
  const errorMessage = useErrorMessage();
  const token = useSearchParams().get("token") ?? "";
  const qc = useQueryClient();
  const verify = useMutation({
    mutationFn: async () => {
      await (await api()).POST("/v1/auth/verify-email", { body: { token } });
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
  const started = useRef(false);
  useEffect(() => {
    if (token && !started.current) {
      started.current = true; // tokens are single-use: never send twice (React strict mode runs effects twice)
      verify.mutate();
    }
  }, [token, verify]);

  return (
    <>
      <AuthHeading title={t("title")} />
      {verify.isSuccess ? (
        <div className="flex flex-col items-center gap-4 text-center">
          <CheckCircle2 className="size-10 text-success" />
          <p className="text-sm text-fg">{t("done")}</p>
          <Link href="/" className="bg-accent-gradient inline-flex h-10 items-center rounded-lg px-5 text-sm font-medium text-white">
            {t("continue")}
          </Link>
        </div>
      ) : verify.isError || !token ? (
        <FormError message={errorMessage(verify.error ?? new ApiError(400, "auth.invalid_token"))} />
      ) : (
        <p className="flex items-center gap-2 text-sm text-muted">
          <Spinner className="size-4" />
          {t("verifying")}
        </p>
      )}
    </>
  );
}
