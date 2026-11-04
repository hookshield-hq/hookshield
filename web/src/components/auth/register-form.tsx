"use client";

import { useState, type FormEvent } from "react";
import { useSearchParams } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { Link, useRouter } from "@/i18n/navigation";
import type { Locale } from "@/i18n/routing";
import { useRegister } from "@/lib/auth";
import { useErrorMessage } from "@/lib/use-error-message";
import { useResetOnHide } from "@/lib/use-reset-on-hide";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { AuthHeading, EMAIL_RE, FormError, PasswordInput, safeNext } from "./fields";
import { GoogleButton } from "./login-form";

export function RegisterForm() {
  const t = useTranslations("auth");
  const v = useTranslations("validation");
  const locale = useLocale() as Locale;
  const errorMessage = useErrorMessage();
  const router = useRouter();
  const next = safeNext(useSearchParams().get("next"));
  const register = useRegister();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [touched, setTouched] = useState(false);
  useResetOnHide(() => {
    setName("");
    setEmail("");
    setPassword("");
    setTouched(false);
    register.reset();
  });

  const emailError = touched && !EMAIL_RE.test(email.trim()) ? v("email") : undefined;
  const passwordError = touched && password.length < 10 ? v("passwordTooShort") : undefined;

  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!EMAIL_RE.test(email.trim()) || password.length < 10) return;
    register.mutate(
      { email: email.trim(), password, name: name.trim() || undefined, locale },
      { onSuccess: () => router.replace(next) },
    );
  }

  return (
    <>
      <AuthHeading title={t("register.title")} subtitle={t("register.subtitle")} />
      <GoogleButton next={next} />
      <form onSubmit={submit} noValidate className="flex flex-col gap-4">
        <FormError message={register.error ? errorMessage(register.error) : null} />
        <Input label={t("name")} autoComplete="name" placeholder={t("namePlaceholder")} value={name} onChange={(e) => setName(e.target.value)} maxLength={100} autoFocus />
        <Input
          label={t("email")}
          type="email"
          autoComplete="email"
          placeholder={t("emailPlaceholder")}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={emailError}
        />
        <PasswordInput
          label={t("password")}
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          hint={t("register.passwordHint")}
          error={passwordError}
        />
        <Button type="submit" size="lg" loading={register.isPending} className="w-full justify-center">
          {t("register.submit")}
        </Button>
      </form>
      <p className="mt-6 text-center text-sm text-muted">
        {t("register.haveAccount")}{" "}
        <Link href="/login" className="font-medium text-accent underline underline-offset-2 hover:decoration-2">
          {t("register.signIn")}
        </Link>
      </p>
    </>
  );
}
