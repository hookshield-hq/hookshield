"use client";

import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { Link, useRouter } from "@/i18n/navigation";
import { googleStartURL, useLogin, useProviders } from "@/lib/auth";
import { apiBaseURL } from "@/lib/api/config";
import { ApiError } from "@/lib/api/errors";
import { useErrorMessage } from "@/lib/use-error-message";
import { useResetOnHide } from "@/lib/use-reset-on-hide";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { AuthHeading, EMAIL_RE, FormError, PasswordInput, safeNext } from "./fields";

function GoogleIcon() {
  return (
    <svg aria-hidden viewBox="0 0 24 24" className="size-4">
      <path fill="#4285F4" d="M23.5 12.3c0-.8-.1-1.6-.2-2.3H12v4.5h6.5a5.6 5.6 0 0 1-2.4 3.6v3h3.9c2.2-2.1 3.5-5.1 3.5-8.8z" />
      <path fill="#34A853" d="M12 24c3.2 0 6-1.1 8-2.9l-3.9-3c-1.1.7-2.5 1.2-4.1 1.2-3.1 0-5.8-2.1-6.7-5H1.3v3.1A12 12 0 0 0 12 24z" />
      <path fill="#FBBC05" d="M5.3 14.3a7.2 7.2 0 0 1 0-4.6V6.6H1.3a12 12 0 0 0 0 10.8z" />
      <path fill="#EA4335" d="M12 4.8c1.8 0 3.3.6 4.6 1.8l3.4-3.4A12 12 0 0 0 1.3 6.6l4 3.1c.9-2.9 3.6-4.9 6.7-4.9z" />
    </svg>
  );
}

export function GoogleButton({ next }: { next: string }) {
  const t = useTranslations("auth");
  const locale = useLocale();
  const providers = useProviders();
  const [href, setHref] = useState<string | null>(null);
  useEffect(() => {
    apiBaseURL()
      .then((base) => setHref(googleStartURL(base, `/${locale}${next === "/" ? "" : next}`)))
      .catch(() => setHref(null));
  }, [locale, next]);
  if (!providers.data?.google || !href) return null;
  return (
    <>
      <a
        href={href}
        className="glass flex h-10 w-full items-center justify-center gap-2 rounded-lg text-sm font-medium text-fg transition hover:bg-hover"
      >
        <GoogleIcon />
        {t("google")}
      </a>
      <div className="my-5 flex items-center gap-3 text-xs text-subtle">
        <span className="h-px flex-1 bg-line" />
        <OrLabel />
        <span className="h-px flex-1 bg-line" />
      </div>
    </>
  );
}

function OrLabel() {
  const t = useTranslations("common");
  return <span>{t("or")}</span>;
}

export function LoginForm() {
  const t = useTranslations("auth");
  const v = useTranslations("validation");
  const errorMessage = useErrorMessage();
  const params = useSearchParams();
  const router = useRouter();
  const next = safeNext(params.get("next"));
  const login = useLogin();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [touched, setTouched] = useState(false);
  const oauthError = params.get("error");
  useResetOnHide(() => {
    setPassword("");
    setTouched(false);
    login.reset();
  });

  const emailError = touched && !EMAIL_RE.test(email.trim()) ? v("email") : undefined;
  const passwordError = touched && !password ? v("required") : undefined;

  function submit(e: FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!EMAIL_RE.test(email.trim()) || !password) return;
    login.mutate(
      { email: email.trim(), password },
      { onSuccess: (user) => router.replace(next, { locale: user.locale }) },
    );
  }

  const failure = login.error ?? (oauthError ? new ApiError(400, oauthError) : null);

  return (
    <>
      <AuthHeading title={t("login.title")} subtitle={t("login.subtitle")} />
      <GoogleButton next={next} />
      <form onSubmit={submit} noValidate className="flex flex-col gap-4">
        <FormError message={failure ? errorMessage(failure) : null} />
        <Input
          label={t("email")}
          type="email"
          autoComplete="email"
          placeholder={t("emailPlaceholder")}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          error={emailError}
          autoFocus
        />
        <div className="flex flex-col gap-1.5">
          <PasswordInput
            label={t("password")}
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            error={passwordError}
          />
          <Link href="/forgot-password" className="self-end text-xs text-accent hover:underline">
            {t("login.forgot")}
          </Link>
        </div>
        <Button type="submit" size="lg" loading={login.isPending} className="w-full justify-center">
          {t("login.submit")}
        </Button>
      </form>
      <p className="mt-6 text-center text-sm text-muted">
        {t("login.noAccount")}{" "}
        <Link href={next === "/" ? "/register" : `/register?next=${encodeURIComponent(next)}`} className="font-medium text-accent underline underline-offset-2 hover:decoration-2">
          {t("login.createAccount")}
        </Link>
      </p>
    </>
  );
}
