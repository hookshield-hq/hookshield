"use client";

import { useState } from "react";
import { useTranslations } from "next-intl";
import { CheckCircle2, Circle, Plus } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { CreateEndpoint } from "@/components/endpoints/create-endpoint";
import { cn } from "@/lib/cn";

/** First-run guide: create an endpoint, send a test event, inspect it. */
export function Onboarding({ hasEndpoint, hasWebhook, firstEndpointId }: { hasEndpoint: boolean; hasWebhook: boolean; firstEndpointId?: string }) {
  const t = useTranslations("dashboard.onboarding");
  const [creating, setCreating] = useState(false);
  const steps = [
    { done: hasEndpoint, title: t("step1"), body: t("step1Body") },
    { done: hasWebhook, title: t("step2"), body: t("step2Body") },
    { done: false, title: t("step3"), body: t("step3Body") },
  ];
  return (
    <Card className="relative mb-6 overflow-hidden p-6">
      <div aria-hidden className="bg-accent-gradient absolute -top-24 -right-24 size-64 rounded-full opacity-20 blur-3xl" />
      <div className="relative flex flex-col gap-6 lg:flex-row lg:items-center lg:justify-between">
        <div className="max-w-md">
          <h2 className="text-lg font-semibold text-fg">{t("title")}</h2>
          <p className="mt-1 text-sm text-muted">{t("body")}</p>
          <div className="mt-4">
            {!hasEndpoint ? (
              <Button onClick={() => setCreating(true)}>
                <Plus className="size-4" />
                {t("create")}
              </Button>
            ) : (
              <Link href={firstEndpointId ? `/sandbox?endpoint=${firstEndpointId}` : "/sandbox"} className="bg-accent-gradient inline-flex h-9 items-center gap-2 rounded-lg px-4 text-sm font-medium text-white shadow-glass transition hover:brightness-110">
                {t("openSandbox")}
              </Link>
            )}
          </div>
        </div>
        <ol className="flex flex-1 flex-col gap-3 lg:max-w-md">
          {steps.map((s, i) => (
            <li key={i} className="flex gap-3">
              {s.done ? <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-success" /> : <Circle className={cn("mt-0.5 size-5 shrink-0", i === steps.findIndex((x) => !x.done) ? "text-accent" : "text-subtle")} />}
              <div>
                <p className={cn("text-sm font-medium", s.done ? "text-muted line-through" : "text-fg")}>{s.title}</p>
                <p className="text-xs text-muted">{s.body}</p>
              </div>
            </li>
          ))}
        </ol>
      </div>
      <CreateEndpoint open={creating} onClose={() => setCreating(false)} />
    </Card>
  );
}
