"use client";

import { useEffect, useRef, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { useQueryClient } from "@tanstack/react-query";
import { Check, CreditCard, ExternalLink, Loader2, Sparkles } from "lucide-react";
import { useBilling, useCheckout, useFeatures, usePortal, type BillingOffer, type BillingOverview } from "@/lib/queries";
import { formatNumber } from "@/lib/format";
import { useErrorMessage } from "@/lib/use-error-message";
import { Badge, type Tone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { ConfirmDialog } from "@/components/ui/confirm";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Skeleton } from "@/components/ui/skeleton";
import { useToast } from "@/components/ui/toast";
import { cn } from "@/lib/cn";

type Sub = NonNullable<BillingOverview["subscription"]>;

const statusTone: Record<Sub["status"], Tone> = {
  active: "success",
  trialing: "info",
  past_due: "warning",
  cancelled: "warning",
  paused: "neutral",
  unpaid: "danger",
  expired: "neutral",
};

/** How long to wait for the provider's webhook after the checkout before giving up polling. */
const ACTIVATION_TIMEOUT_MS = 120_000;

/** True when the subscription gives the user the paid plan right now. */
function inEffect(sub: Sub | null | undefined) {
  if (!sub || (sub.status !== "active" && sub.status !== "trialing")) return false;
  return !sub.ends_at || new Date(sub.ends_at).getTime() > Date.now();
}

function money(locale: string, cents: number, currency: string) {
  return new Intl.NumberFormat(locale, { style: "currency", currency, maximumFractionDigits: cents % 100 ? 2 : 0 }).format(cents / 100);
}

function date(locale: string, iso: string) {
  return new Intl.DateTimeFormat(locale, { dateStyle: "long" }).format(new Date(iso));
}

/** Leaves the page for the provider (checkout or customer portal). */
function go(url: string) {
  window.location.assign(url);
}

function Usage({ b }: { b: BillingOverview }) {
  const t = useTranslations("billing");
  const locale = useLocale();
  const u = b.usage;
  const pct = u.included ? Math.min(100, (u.requests / u.included) * 100) : 0;
  const tone = !u.included ? "bg-accent-gradient" : pct >= 100 ? "bg-danger" : pct >= 80 ? "bg-warning" : "bg-accent-gradient";
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("usage.title")}</CardTitle>
          <p className="mt-0.5 text-xs text-muted">{t("usage.resets", { date: date(locale, u.period_end) })}</p>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-3">
        <div className="flex items-baseline justify-between gap-4">
          <span className="text-2xl font-semibold text-fg tabular-nums">{formatNumber(locale, u.requests)}</span>
          <span className="text-sm text-muted">{u.included == null ? t("usage.unlimited") : t("usage.of", { included: formatNumber(locale, u.included) })}</span>
        </div>
        {u.included != null && (
          <div className="h-2 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)} aria-label={t("usage.title")}>
            <div className={cn("h-full rounded-full transition-all", tone)} style={{ width: `${Math.max(1, pct)}%` }} />
          </div>
        )}
        {u.included != null && u.requests >= u.included ? (
          <p className="text-xs text-danger">{u.hard_limit ? t("usage.blocked") : t("usage.overage")}</p>
        ) : u.included != null && pct >= 80 ? (
          <p className="text-xs text-warning">{u.hard_limit ? t("usage.nearBlocked") : t("usage.nearOverage")}</p>
        ) : null}
      </CardBody>
    </Card>
  );
}

function PlanCard({ offer, current, subscribed, onChoose, busy }: { offer: BillingOffer; current: boolean; subscribed: boolean; onChoose: (o: BillingOffer) => void; busy: boolean }) {
  const t = useTranslations("billing");
  const plans = useTranslations("settings.plans");
  const locale = useLocale();
  const l = offer.limits;
  const popular = offer.id === "pro";
  const features = [
    l.monthly_requests == null ? t("features.unlimited") : t("features.requests", { n: formatNumber(locale, l.monthly_requests) }),
    l.max_retries == null ? t("features.customRetries") : t("features.retries", { n: l.max_retries }),
    t("features.retention", { days: Math.round(l.retention_hours / 24) }),
    offer.overage_price_cents > 0
      ? t("features.overage", { price: money(locale, offer.overage_price_cents, offer.currency), unit: formatNumber(locale, offer.overage_unit) })
      : l.hard_limit
        ? t("features.hardLimit")
        : null,
  ].filter(Boolean) as string[];

  let action: React.ReactNode;
  if (current) {
    action = (
      <Button variant="secondary" disabled className="w-full justify-center">
        <Check className="size-4" />
        {t("current")}
      </Button>
    );
  } else if (offer.purchasable) {
    action = (
      <Button className="w-full justify-center" loading={busy} onClick={() => onChoose(offer)}>
        {subscribed ? t("switchTo", { plan: plans(offer.id) }) : t("upgrade", { plan: plans(offer.id) })}
      </Button>
    );
  } else if (offer.contact_url) {
    action = (
      <a href={offer.contact_url} className="glass inline-flex h-9 w-full items-center justify-center gap-2 rounded-lg px-4 text-sm font-medium text-fg transition hover:bg-hover">
        {t("contact")}
      </a>
    );
  } else if (subscribed && offer.monthly_price_cents === 0) {
    action = <p className="text-center text-xs text-muted">{t("downgradeHint")}</p>;
  }

  return (
    <Card className={cn("relative flex flex-col p-5", popular && "ring-2 ring-accent/50", current && "ring-2 ring-success/50")}>
      {popular && !current && (
        <Badge tone="info" className="absolute -top-2.5 left-5">
          <Sparkles className="size-3" />
          {t("popular")}
        </Badge>
      )}
      <h3 className="text-base font-semibold text-fg">{plans(offer.id)}</h3>
      <div className="mt-2 mb-4 flex items-baseline gap-1">
        {offer.monthly_price_cents > 0 ? (
          <>
            <span className="text-3xl font-semibold tracking-tight text-fg">{money(locale, offer.monthly_price_cents, offer.currency)}</span>
            <span className="text-sm text-muted">{t("perMonth")}</span>
          </>
        ) : offer.purchasable || offer.id === "developer" ? (
          <span className="text-3xl font-semibold tracking-tight text-fg">{t("free")}</span>
        ) : (
          <span className="text-3xl font-semibold tracking-tight text-fg">{t("custom")}</span>
        )}
      </div>
      <ul className="mb-5 flex flex-1 flex-col gap-2 text-sm text-muted">
        {features.map((f) => (
          <li key={f} className="flex gap-2">
            <Check className="mt-0.5 size-4 shrink-0 text-success" />
            {f}
          </li>
        ))}
      </ul>
      {action}
    </Card>
  );
}

export function BillingPage() {
  const t = useTranslations("billing");
  const plans = useTranslations("settings.plans");
  const common = useTranslations("common");
  const locale = useLocale();
  const toast = useToast();
  const qc = useQueryClient();
  const errorMessage = useErrorMessage();
  const params = useSearchParams();
  const features = useFeatures();
  // Back from the checkout (?checkout=success): poll until the provider's
  // webhook has activated the subscription, then clean the URL.
  const fromCheckout = params.get("checkout") === "success";
  // Polling stops after ACTIVATION_TIMEOUT_MS; a late webhook is still picked up on the next refetch.
  const [timedOut, setTimedOut] = useState(false);
  const billing = useBilling({
    enabled: features.data?.billing === true,
    refetchInterval: (data) => (fromCheckout && !timedOut && !inEffect(data?.subscription) ? 2000 : false),
  });
  // An old expired, paused or unpaid subscription is not an activation: wait for one in effect.
  const activated = fromCheckout && inEffect(billing.data?.subscription);
  const pending = fromCheckout && !activated;
  const waiting = pending && !timedOut;
  const stillProcessing = pending && timedOut;
  useEffect(() => {
    if (!pending) return;
    const id = window.setTimeout(() => setTimedOut(true), ACTIVATION_TIMEOUT_MS);
    return () => window.clearTimeout(id);
  }, [pending]);
  const checkout = useCheckout();
  const portal = usePortal();
  const [switching, setSwitching] = useState<BillingOffer | null>(null);
  const announced = useRef(false);
  useEffect(() => {
    if (!activated || announced.current) return;
    announced.current = true;
    window.history.replaceState(window.history.state, "", window.location.pathname);
    void qc.invalidateQueries({ queryKey: ["me"] });
    toast.success(t("activated"));
  }, [activated, qc, toast, t]);

  const choose = (o: BillingOffer) => {
    if (billing.data?.subscription && ["active", "trialing", "past_due", "cancelled"].includes(billing.data.subscription.status)) {
      setSwitching(o);
      return;
    }
    checkout.mutate(o.id as "pro" | "business", { onSuccess: (r) => r.url && go(r.url), onError: (err) => toast.error(errorMessage(err)) });
  };

  if (features.isError && !features.data) {
    return (
      <div className="mx-auto max-w-5xl">
        <PageHeader title={t("title")} />
        <Card>
          <EmptyState title={common("errorTitle")} body={errorMessage(features.error)} action={<Button onClick={() => features.refetch()}>{common("retry")}</Button>} />
        </Card>
      </div>
    );
  }

  if (features.data && !features.data.billing) {
    return (
      <div className="mx-auto max-w-5xl">
        <PageHeader title={t("title")} />
        <Card>
          <EmptyState icon={<CreditCard className="size-6" />} title={t("unavailable")} body={t("unavailableBody")} />
        </Card>
      </div>
    );
  }

  const b = billing.data;
  const sub = b?.subscription ?? null;
  const usable = !!sub && ["active", "trialing", "past_due", "cancelled"].includes(sub.status);

  return (
    <div className="mx-auto max-w-6xl">
      <PageHeader
        title={t("title")}
        description={t("subtitle")}
        actions={
          sub && (
            <Button variant="secondary" loading={portal.isPending} onClick={() => portal.mutate(undefined, { onSuccess: go, onError: (err) => toast.error(errorMessage(err)) })}>
              <ExternalLink className="size-4" />
              {t("manage")}
            </Button>
          )
        }
      />

      {waiting && (
        <div role="status" className="mb-6 flex items-center gap-3 rounded-xl border border-info/30 bg-info-bg px-4 py-3 text-sm text-info">
          <Loader2 className="size-4 animate-spin" />
          {t("activating")}
        </div>
      )}

      {stillProcessing && (
        <div role="status" className="mb-6 rounded-xl border border-info/30 bg-info-bg px-4 py-3 text-sm text-info">
          {t("stillProcessing")}
        </div>
      )}

      {!b && billing.isError ? (
        <Card>
          <EmptyState title={common("errorTitle")} body={errorMessage(billing.error)} action={<Button onClick={() => billing.refetch()}>{common("retry")}</Button>} />
        </Card>
      ) : !b ? (
        <div className="flex flex-col gap-4" aria-busy="true">
          <Skeleton className="h-32 w-full" />
          <Skeleton className="h-72 w-full" />
          <span className="sr-only">{common("loading")}</span>
        </div>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader>
                <CardTitle>{t("currentPlan")}</CardTitle>
                {sub?.test_mode && <Badge tone="warning">{t("testMode")}</Badge>}
              </CardHeader>
              <CardBody className="flex flex-col gap-3">
                <div className="flex flex-wrap items-center gap-3">
                  <span className="text-2xl font-semibold text-fg">{plans.has(b.plan) ? plans(b.plan) : b.plan}</span>
                  {sub && <Badge tone={statusTone[sub.status]}>{t(`status.${sub.status}`)}</Badge>}
                </div>
                <p className="text-sm text-muted">
                  {!sub
                    ? t("freeNote")
                    : sub.status === "cancelled" && sub.ends_at
                      ? t("endsOn", { date: date(locale, sub.ends_at), plan: plans(sub.plan) })
                      : sub.status === "past_due"
                        ? t("pastDue")
                        : usable && sub.renews_at
                          ? t("renewsOn", { date: date(locale, sub.renews_at) })
                          : t("inactive")}
                </p>
              </CardBody>
            </Card>
            <Usage b={b} />
          </div>

          <h2 className="mb-3 text-sm font-semibold text-fg">{t("plans")}</h2>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            {b.offers.map((o) => (
              <PlanCard key={o.id} offer={o} current={o.id === b.plan} subscribed={usable} busy={checkout.isPending && checkout.variables === o.id} onChoose={choose} />
            ))}
          </div>
          <p className="mt-4 text-xs text-subtle">{t("note")}</p>
        </>
      )}

      <ConfirmDialog
        open={!!switching}
        tone="primary"
        onClose={() => setSwitching(null)}
        title={t("switchTitle", { plan: switching ? plans(switching.id) : "" })}
        body={t("switchBody")}
        confirmLabel={t("switchConfirm")}
        loading={checkout.isPending}
        onConfirm={() =>
          switching &&
          checkout.mutate(switching.id as "pro" | "business", {
            onSuccess: (r) => {
              setSwitching(null);
              if (r.url) go(r.url);
              else toast.success(t("switched", { plan: plans(switching.id) }));
            },
            onError: (err) => {
              setSwitching(null);
              toast.error(errorMessage(err));
            },
          })
        }
      />
    </div>
  );
}
