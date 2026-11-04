/* eslint-disable @next/next/no-img-element -- static export: plain <img> with explicit sizes */
import Link from "next/link";
import {
  Activity,
  ArrowRight,
  BellRing,
  BookOpen,
  Check,
  Code2,
  Command,
  KeyRound,
  Languages,
  Lock,
  RotateCcw,
  Send,
  Server,
  ShieldAlert,
  ShieldCheck,
  Star,
  Wand2,
} from "lucide-react";
import { APP_URL, CLI_INSTALL, CONTACT, DEPLOY, GITHUB_URL } from "@/site";
import { formatNumber, formatUSD, getT, type Locale, type T } from "@/i18n";
import { plans, type Plan } from "@/pricing";
import { CopyButton } from "./copy-button";

/** Measured ingest p99 at 200 req/s (docs/PERFORMANCE.md). Update with the benchmark. */
const INGEST_P99_MS = 15.9;

export function Landing({ locale }: { locale: Locale }) {
  const t = getT(locale);
  return (
    <main id="content">
      <Hero locale={locale} t={t} />
      <Pipeline locale={locale} t={t} />
      <Features t={t} />
      <Screens locale={locale} t={t} />
      <Cli t={t} />
      <SelfHost t={t} />
      <Pricing locale={locale} t={t} />
      <Security t={t} />
      <Faq t={t} />
      <FinalCta locale={locale} t={t} />
    </main>
  );
}

function SectionHeading({ eyebrow, title, subtitle, id }: { eyebrow: string; title: string; subtitle?: string; id: string }) {
  return (
    <div className="mx-auto max-w-2xl text-center">
      <p className="text-accent-gradient text-sm font-semibold uppercase tracking-wider">{eyebrow}</p>
      <h2 id={id} className="mt-3 text-3xl font-semibold tracking-tight text-balance sm:text-4xl">
        {title}
      </h2>
      {subtitle && <p className="mt-4 text-pretty text-muted">{subtitle}</p>}
    </div>
  );
}

/** A dashboard screenshot in the page's language, swapped with the theme (made by web/tests/e2e/landing-shots.spec.ts). */
function Screenshot({ name, locale, alt, priority = false }: { name: string; locale: Locale; alt: string; priority?: boolean }) {
  // Only the default (dark) theme's image loads eagerly; a hidden lazy image is never fetched.
  return (
    <>
      <img src={`/screens/${name}-dark-${locale}.webp`} alt={alt} width={1440} height={900} loading={priority ? "eager" : "lazy"} className="hidden w-full dark:block" />
      <img src={`/screens/${name}-light-${locale}.webp`} alt={alt} width={1440} height={900} loading="lazy" className="w-full dark:hidden" />
    </>
  );
}

function Hero({ locale, t }: { locale: Locale; t: T }) {
  return (
    <section className="relative overflow-hidden" aria-labelledby="hero-title">
      <div className="hero-glow pointer-events-none absolute inset-0" aria-hidden />
      <div className="grid-fade pointer-events-none absolute inset-0 opacity-60" aria-hidden />
      <div className="relative mx-auto max-w-6xl px-5 pb-16 pt-20 text-center sm:pt-28">
        <a
          href={GITHUB_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="glass inline-flex items-center gap-2 rounded-full px-3.5 py-1.5 text-xs font-medium text-muted transition hover:text-fg"
        >
          <Star className="size-3.5 text-accent" aria-hidden />
          {t("hero.badge")}
          <ArrowRight className="size-3.5" aria-hidden />
        </a>
        <h1 id="hero-title" className="mx-auto mt-6 max-w-4xl text-5xl font-semibold tracking-tight text-balance sm:text-6xl lg:text-7xl">
          {t("hero.title")}
        </h1>
        <p className="mx-auto mt-6 max-w-2xl text-lg text-pretty text-muted">{t("hero.subtitle")}</p>
        <div className="mt-9 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <a
            href={`${APP_URL}/${locale}/register`}
            className="bg-accent-gradient inline-flex items-center gap-2 rounded-xl px-5 py-3 font-medium text-white shadow-lg shadow-violet-500/20 transition hover:opacity-90"
          >
            {t("hero.primary")}
            <ArrowRight className="size-4" aria-hidden />
          </a>
          <a href="#self-host" className="glass inline-flex items-center gap-2 rounded-xl px-5 py-3 font-medium transition hover:bg-hover">
            <Server className="size-4" aria-hidden />
            {t("hero.secondary")}
          </a>
        </div>
        <p className="mt-4 text-sm text-subtle">{t("hero.note")}</p>
        <div className="glass mx-auto mt-14 max-w-5xl overflow-hidden rounded-2xl p-1.5">
          <div className="overflow-hidden rounded-xl border border-line">
            <Screenshot name="dashboard" locale={locale} alt={t("hero.screenshotAlt")} priority />
          </div>
        </div>
      </div>
    </section>
  );
}

const pipelineSteps = [
  { key: "guard", icon: ShieldCheck },
  { key: "transform", icon: Wand2 },
  { key: "encrypt", icon: Lock },
  { key: "deliver", icon: Send },
  { key: "alert", icon: BellRing },
] as const;

function Pipeline({ locale, t }: { locale: Locale; t: T }) {
  return (
    <section id="how-it-works" className="mx-auto max-w-6xl px-5 py-24" aria-labelledby="pipeline-title">
      <SectionHeading id="pipeline-title" eyebrow={t("pipeline.eyebrow")} title={t("pipeline.title")} subtitle={t("pipeline.subtitle")} />
      <ol className="mt-14 grid gap-4 md:grid-cols-5">
        {pipelineSteps.map(({ key, icon: Icon }, i) => (
          <li key={key} className="glass relative rounded-2xl p-5">
            <div className="flex items-center gap-3">
              <span className="bg-accent-gradient inline-flex size-9 items-center justify-center rounded-xl text-white">
                <Icon className="size-4.5" aria-hidden />
              </span>
              <span className="font-mono text-xs text-subtle">0{i + 1}</span>
            </div>
            <h3 className="mt-4 font-semibold">{t(`pipeline.steps.${key}.title`)}</h3>
            <p className="mt-2 text-sm text-muted">{t(`pipeline.steps.${key}.body`)}</p>
          </li>
        ))}
      </ol>
      <div className="mt-8 flex flex-col items-center gap-1 text-center">
        <p className="inline-flex items-center gap-2 font-medium">
          <Activity className="size-4 text-success" aria-hidden />
          {t("pipeline.latency", { ms: new Intl.NumberFormat(locale).format(INGEST_P99_MS) })}
        </p>
        <p className="text-sm text-subtle">
          {t("pipeline.latencyNote")}{" "}
          <Link href="/docs/performance/" className="text-accent underline underline-offset-2">
            {t("pipeline.latencyLink")}
          </Link>
        </p>
      </div>
    </section>
  );
}

const featureItems = [
  { key: "debugger", icon: Activity },
  { key: "replay", icon: RotateCcw },
  { key: "secrets", icon: KeyRound },
  { key: "retries", icon: Send },
  { key: "ssrf", icon: ShieldAlert },
  { key: "keyboard", icon: Command },
  { key: "languages", icon: Languages },
  { key: "api", icon: Code2 },
] as const;

function Features({ t }: { t: T }) {
  return (
    <section id="features" className="border-y border-line bg-surface/40 py-24" aria-labelledby="features-title">
      <div className="mx-auto max-w-6xl px-5">
        <SectionHeading id="features-title" eyebrow={t("features.eyebrow")} title={t("features.title")} />
        <ul className="mt-14 grid gap-px overflow-hidden rounded-2xl border border-line bg-line sm:grid-cols-2 lg:grid-cols-4">
          {featureItems.map(({ key, icon: Icon }) => (
            <li key={key} className="bg-bg p-6">
              <Icon className="size-5 text-accent" aria-hidden />
              <h3 className="mt-4 font-semibold">{t(`features.items.${key}.title`)}</h3>
              <p className="mt-2 text-sm text-muted">{t(`features.items.${key}.body`)}</p>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

function Screens({ locale, t }: { locale: Locale; t: T }) {
  const shots = ["debugger", "transform", "endpoints"] as const;
  return (
    <section className="mx-auto max-w-6xl px-5 py-24" aria-labelledby="screens-title">
      <SectionHeading id="screens-title" eyebrow={t("screens.eyebrow")} title={t("screens.title")} />
      <div className="mt-14 grid gap-8 lg:grid-cols-3">
        {shots.map((name) => (
          <figure key={name} className="glass overflow-hidden rounded-2xl p-1.5">
            <div className="overflow-hidden rounded-xl border border-line">
              <Screenshot name={name} locale={locale} alt="" />
            </div>
            <figcaption className="px-3 py-3 text-sm text-muted">{t(`screens.${name}`)}</figcaption>
          </figure>
        ))}
      </div>
    </section>
  );
}

function Cli({ t }: { t: T }) {
  return (
    <section className="border-y border-line bg-surface/40 py-24" aria-labelledby="cli-title">
      <div className="mx-auto grid max-w-6xl items-center gap-12 px-5 lg:grid-cols-2">
        <div>
          <p className="text-accent-gradient text-sm font-semibold uppercase tracking-wider">{t("cli.eyebrow")}</p>
          <h2 id="cli-title" className="mt-3 text-3xl font-semibold tracking-tight sm:text-4xl">
            {t("cli.title")}
          </h2>
          <p className="mt-4 text-pretty text-muted">{t("cli.body")}</p>
          <p className="mt-6 text-sm font-medium">{t("cli.install")}</p>
          <div className="mt-2 flex items-center gap-2 rounded-xl border border-line bg-surface-strong p-1.5 pl-4">
            <code tabIndex={0} className="min-w-0 flex-1 overflow-x-auto font-mono text-xs whitespace-nowrap">{CLI_INSTALL}</code>
            <CopyButton value={CLI_INSTALL} label={t("cli.copy")} />
          </div>
          <Link href="/docs/cli/" className="mt-5 inline-flex items-center gap-1.5 text-sm font-medium text-accent hover:underline">
            <BookOpen className="size-4" aria-hidden />
            {t("cli.docs")}
          </Link>
        </div>
        <Terminal />
      </div>
    </section>
  );
}

/** Output of `hookshield listen`, as printed by the CLI (docs/CLI.md). The CLI speaks English. */
const CLI_INTRO = [
  { text: "$ ", cls: "text-slate-400" },
  { text: "hookshield listen Stripe --forward localhost:3000/webhooks\n" },
  { text: "Ready!", cls: "text-emerald-400" },
  { text: " Forwarding webhooks of Stripe (ep_k3j7x2m4q5ab) → http://localhost:3000/webhooks\n" },
  { text: "  Webhook URL  https://in.hookshield.dev/v1/ep_k3j7x2m4q5ab\n" },
  { text: "  HookShield still delivers them to each endpoint's destination too. Ctrl+C to stop.\n\n" },
];
const CLI_EVENTS = [
  { time: "20:34:12", event: "invoice.paid", id: "01a1a3c3…71d5", status: "200 OK", ok: true, ms: 12 },
  { time: "20:34:15", event: "customer.created", id: "01a1a3c3…f29b", status: "500 Internal Server Error", ok: false, ms: 3 },
  { time: "20:34:19", event: "charge.succeeded", id: "01a1a3c4…0a7e", status: "200 OK", ok: true, ms: 9 },
];

function Terminal() {
  return (
    <div className="overflow-hidden rounded-2xl border border-line bg-[#0b1020] shadow-2xl" translate="no">
      <div className="flex items-center gap-1.5 border-b border-white/10 px-4 py-3" aria-hidden>
        <span className="size-3 rounded-full bg-white/15" />
        <span className="size-3 rounded-full bg-white/15" />
        <span className="size-3 rounded-full bg-white/15" />
      </div>
      <pre className="overflow-x-auto p-5 font-mono text-[12.5px] leading-6 text-slate-300" tabIndex={0}>
        {CLI_INTRO.map((s, i) => (
          <span key={i} className={s.cls}>
            {s.text}
          </span>
        ))}
        {CLI_EVENTS.map((e) => (
          <span key={e.time}>
            <span className="text-slate-400">{e.time}</span>
            {"  "}
            <span className="text-cyan-300">{"-->"}</span>
            {` ${e.event} `}
            <span className="text-slate-400">{`[ep_k3j7x2m4q5ab ${e.id}]`}</span>
            {"  "}
            <span className={e.ok ? "text-emerald-400" : "text-red-400"}>{`<-- ${e.status}`}</span>
            <span className="text-slate-400">{` (${e.ms} ms)`}</span>
            {"\n"}
          </span>
        ))}
      </pre>
    </div>
  );
}

const COMPOSE_STEPS = [`git clone ${GITHUB_URL}`, "cd hookshield/deploy", "./setup.sh hooks.example.com", "docker compose up -d"];

function SelfHost({ t }: { t: T }) {
  const buttons = [
    { href: DEPLOY.render, label: t("selfHost.render"), external: true },
    { href: DEPLOY.railway, label: t("selfHost.railway"), external: true },
    { href: DEPLOY.coolify, label: t("selfHost.coolify") },
  ].filter((b) => b.href); // Railway appears once its template is published
  return (
    <section id="self-host" className="mx-auto max-w-6xl px-5 py-24" aria-labelledby="self-host-title">
      <SectionHeading id="self-host-title" eyebrow={t("selfHost.eyebrow")} title={t("selfHost.title")} subtitle={t("selfHost.body")} />
      <div className="mx-auto mt-12 grid max-w-4xl gap-6 md:grid-cols-2">
        <div className="glass flex flex-col gap-3 rounded-2xl p-6">
          {buttons.map((b) => (
            <a
              key={b.label}
              href={b.href}
              {...(b.external ? { target: "_blank", rel: "noopener noreferrer" } : {})}
              className="flex items-center justify-between rounded-xl border border-line bg-surface-strong px-4 py-3 font-medium transition hover:border-accent"
            >
              {b.label}
              <ArrowRight className="size-4 text-muted" aria-hidden />
            </a>
          ))}
          <p className="mt-auto pt-2 text-sm text-muted">{t("selfHost.oneClickNote")}</p>
        </div>
        <div className="glass flex flex-col rounded-2xl p-6">
          <h3 className="font-semibold">{t("selfHost.compose")}</h3>
          <p className="mt-1 text-sm text-muted">{t("selfHost.composeNote")}</p>
          <pre className="mt-4 overflow-x-auto rounded-xl border border-line bg-surface-strong p-4 font-mono text-xs leading-6" tabIndex={0}>
            <span className="text-subtle">{`# ${t("selfHost.composeComment")}`}</span>
            <span translate="no">{COMPOSE_STEPS.map((step) => `\n${step}`).join("")}</span>
          </pre>
          <a href={DEPLOY.compose} className="mt-auto inline-flex items-center gap-1.5 pt-4 text-sm font-medium text-accent hover:underline">
            <BookOpen className="size-4" aria-hidden />
            {t("selfHost.guide")}
          </a>
        </div>
      </div>
    </section>
  );
}

function planFeatures(plan: Plan, locale: Locale, t: T): string[] {
  const out: string[] = [];
  out.push(plan.requests === null ? t("pricing.unlimitedRequests") : t("pricing.requests", { count: formatNumber(locale, plan.requests) }));
  if (plan.hardLimit) out.push(t("pricing.hardLimit"));
  else if (plan.overageCentsPer10k) out.push(t("pricing.overage", { price: formatUSD(locale, plan.overageCentsPer10k) }));
  out.push(plan.maxRetries === null ? t("pricing.unlimitedRetries") : t("pricing.retries", { count: plan.maxRetries }));
  if (plan.id === "enterprise") out.push(t("pricing.customRetention"));
  else if (plan.retentionHours === 24) out.push(t("pricing.retention24h"));
  else out.push(t("pricing.retention", { days: t("pricing.days", { count: plan.retentionHours / 24 }) }));
  out.push(t("pricing.everything"));
  return out;
}

function Pricing({ locale, t }: { locale: Locale; t: T }) {
  return (
    <section id="pricing" className="border-y border-line bg-surface/40 py-24" aria-labelledby="pricing-title">
      <div className="mx-auto max-w-6xl px-5">
        <SectionHeading id="pricing-title" eyebrow={t("pricing.eyebrow")} title={t("pricing.title")} subtitle={t("pricing.subtitle")} />
        <ul className="mt-14 grid gap-5 md:grid-cols-2 lg:grid-cols-4">
          {plans.map((plan) => {
            const href = plan.id === "enterprise" ? CONTACT.sales : `${APP_URL}/${locale}/register`;
            return (
              <li
                key={plan.id}
                className={`relative flex flex-col rounded-2xl p-6 ${plan.popular ? "border-2 border-accent bg-bg shadow-xl shadow-violet-500/10" : "glass"}`}
              >
                {plan.popular && (
                  <span className="bg-accent-gradient absolute -top-3 left-6 rounded-full px-2.5 py-0.5 text-xs font-medium text-white">
                    {t("pricing.popular")}
                  </span>
                )}
                <h3 className="font-semibold">{t(`pricing.plans.${plan.id}`)}</h3>
                <p className="mt-1 min-h-10 text-sm text-muted">{t(`pricing.taglines.${plan.id}`)}</p>
                <p className="mt-5 flex items-baseline gap-1">
                  <span className="text-4xl font-semibold tracking-tight">
                    {plan.monthlyCents === null ? t("pricing.custom") : formatUSD(locale, plan.monthlyCents)}
                  </span>
                  {plan.monthlyCents !== null && <span className="text-sm text-muted">{t("pricing.perMonth")}</span>}
                </p>
                <ul className="mt-6 space-y-2.5 text-sm">
                  {planFeatures(plan, locale, t).map((f) => (
                    <li key={f} className="flex gap-2">
                      <Check className="mt-0.5 size-4 shrink-0 text-success" aria-hidden />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>
                <a
                  href={href}
                  className={`mt-8 rounded-xl px-4 py-2.5 text-center text-sm font-medium transition ${
                    plan.popular ? "bg-accent-gradient text-white hover:opacity-90" : "border border-line bg-surface-strong hover:border-accent"
                  }`}
                >
                  {t(`pricing.cta.${plan.id}`)}
                </a>
              </li>
            );
          })}
        </ul>
        <div className="glass mt-6 flex flex-col items-start gap-4 rounded-2xl p-6 sm:flex-row sm:items-center">
          <Server className="size-6 shrink-0 text-accent" aria-hidden />
          <div className="flex-1">
            <h3 className="font-semibold">
              {t("pricing.selfHosted.title")} · <span className="text-success">{t("pricing.selfHosted.price")}</span>
            </h3>
            <p className="mt-1 text-sm text-muted">{t("pricing.selfHosted.body")}</p>
          </div>
          <a href="#self-host" className="rounded-xl border border-line bg-surface-strong px-4 py-2.5 text-sm font-medium transition hover:border-accent">
            {t("pricing.selfHosted.cta")}
          </a>
        </div>
        <p className="mt-6 text-center text-sm text-subtle">{t("pricing.billingNote")}</p>
      </div>
    </section>
  );
}

function Security({ t }: { t: T }) {
  const items = ["encryption", "tokens", "ssrf", "sessions", "audit", "open"] as const;
  return (
    <section className="mx-auto max-w-6xl px-5 py-24" aria-labelledby="security-title">
      <SectionHeading id="security-title" eyebrow={t("security.eyebrow")} title={t("security.title")} />
      <ul className="mx-auto mt-12 grid max-w-4xl gap-4 sm:grid-cols-2">
        {items.map((key) => (
          <li key={key} className="flex gap-3 rounded-xl border border-line p-4">
            <ShieldCheck className="mt-0.5 size-5 shrink-0 text-accent" aria-hidden />
            <span className="text-sm">{t(`security.items.${key}`)}</span>
          </li>
        ))}
      </ul>
      <p className="mt-8 text-center">
        <Link href="/docs/security/" className="inline-flex items-center gap-1.5 text-sm font-medium text-accent hover:underline">
          {t("security.policy")}
          <ArrowRight className="size-4" aria-hidden />
        </Link>
      </p>
    </section>
  );
}

function Faq({ t }: { t: T }) {
  const items = ["free", "down", "guarantees", "storage", "providers", "latency"] as const;
  return (
    <section id="faq" className="border-t border-line py-24" aria-labelledby="faq-title">
      <div className="mx-auto max-w-3xl px-5">
        <h2 id="faq-title" className="text-center text-3xl font-semibold tracking-tight">
          {t("faq.title")}
        </h2>
        <div className="mt-10 divide-y divide-line rounded-2xl border border-line">
          {items.map((key) => (
            <details key={key} className="group p-5 [&_summary::-webkit-details-marker]:hidden">
              <summary className="flex cursor-pointer list-none items-center justify-between gap-4 font-medium">
                {t(`faq.items.${key}.q`)}
                <span className="text-muted transition group-open:rotate-45" aria-hidden>
                  +
                </span>
              </summary>
              <p className="mt-3 text-sm text-pretty text-muted">{t(`faq.items.${key}.a`)}</p>
            </details>
          ))}
        </div>
      </div>
    </section>
  );
}

function FinalCta({ locale, t }: { locale: Locale; t: T }) {
  return (
    <section className="relative overflow-hidden border-t border-line" aria-labelledby="cta-title">
      <div className="hero-glow pointer-events-none absolute inset-0" aria-hidden />
      <div className="relative mx-auto max-w-3xl px-5 py-24 text-center">
        <h2 id="cta-title" className="text-3xl font-semibold tracking-tight text-balance sm:text-4xl">
          {t("cta.title")}
        </h2>
        <p className="mt-4 text-pretty text-muted">{t("cta.body")}</p>
        <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
          <a
            href={`${APP_URL}/${locale}/register`}
            className="bg-accent-gradient inline-flex items-center gap-2 rounded-xl px-5 py-3 font-medium text-white transition hover:opacity-90"
          >
            {t("cta.primary")}
            <ArrowRight className="size-4" aria-hidden />
          </a>
          <Link href="/docs/" className="glass inline-flex items-center gap-2 rounded-xl px-5 py-3 font-medium transition hover:bg-hover">
            <BookOpen className="size-4" aria-hidden />
            {t("cta.secondary")}
          </Link>
        </div>
      </div>
    </section>
  );
}
