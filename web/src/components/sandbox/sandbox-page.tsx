"use client";

import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "next/navigation";
import { useLocale, useTranslations } from "next-intl";
import { BookOpen, ExternalLink, FlaskConical, Send } from "lucide-react";
import { Link } from "@/i18n/navigation";
import { apiBaseURL } from "@/lib/api/config";
import { useEndpoints, useLog, useTestEndpoint, type Endpoint } from "@/lib/queries";
import { formatMs, formatRelative } from "@/lib/format";
import { useErrorMessage } from "@/lib/use-error-message";
import { Badge, logTone, statusTone } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardBody, CardHeader, CardTitle } from "@/components/ui/card";
import { CodeBlock, parseJSON, prettyJSON } from "@/components/ui/code";
import { EmptyState } from "@/components/ui/empty-state";
import { Select, Textarea } from "@/components/ui/field";
import { fromKV, KeyValueEditor, type KV } from "@/components/ui/kv-editor";
import { PageHeader } from "@/components/ui/page-header";
import { Segmented } from "@/components/ui/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs } from "@/components/ui/tabs";
import { CopyField } from "@/components/endpoints/shared";

type Method = "POST" | "PUT" | "PATCH";

const TEMPLATES: Record<string, { headers: KV[]; body: unknown }> = {
  generic: { headers: [{ key: "Content-Type", value: "application/json" }], body: { type: "order.created", id: "ord_123", amount: 4900, currency: "usd" } },
  stripe: {
    headers: [
      { key: "Content-Type", value: "application/json" },
      { key: "User-Agent", value: "Stripe/1.0 (+https://stripe.com/docs/webhooks)" },
    ],
    body: { id: "evt_1Q2w3E", object: "event", type: "invoice.paid", data: { object: { id: "in_9", object: "invoice", amount_paid: 4900, currency: "usd", customer: "cus_42" } } },
  },
  github: {
    headers: [
      { key: "Content-Type", value: "application/json" },
      { key: "X-GitHub-Event", value: "push" },
      { key: "User-Agent", value: "GitHub-Hookshot/abc123" },
    ],
    body: { ref: "refs/heads/main", repository: { full_name: "acme/api" }, pusher: { name: "ana" }, commits: [{ id: "a1b2c3", message: "Fix webhook retries" }] },
  },
  shopify: {
    headers: [
      { key: "Content-Type", value: "application/json" },
      { key: "X-Shopify-Topic", value: "orders/create" },
    ],
    body: { id: 820982911946154500, email: "jon@example.com", total_price: "199.00", currency: "USD", line_items: [{ sku: "IPOD-342", quantity: 1 }] },
  },
};

function snippets(e: Endpoint, method: Method, headers: Record<string, string>, body: string) {
  const auth = e.auth.mode === "token" ? { [e.auth.header]: "<token>" } : e.auth.mode === "hmac" ? { [e.auth.hmac_header ?? "X-HookShield-Signature"]: "sha256=<hmac>" } : {};
  const all = { ...headers, ...auth };
  const compact = (() => {
    const p = parseJSON(body);
    return p.ok && p.value !== undefined ? JSON.stringify(p.value) : body;
  })();
  const curl = [`curl -X ${method} '${e.ingest_url}'`, ...Object.entries(all).map(([k, v]) => `  -H '${k}: ${v}'`), `  -d '${compact.replace(/'/g, "'\\''")}'`].join(" \\\n");
  const node = `const res = await fetch(${JSON.stringify(e.ingest_url)}, {
  method: ${JSON.stringify(method)},
  headers: ${JSON.stringify(all, null, 2).replace(/\n/g, "\n  ")},
  body: JSON.stringify(${compact}),
});
console.log(res.status); // 202 = accepted`;
  const python = `import requests

res = requests.${method.toLowerCase()}(
    ${JSON.stringify(e.ingest_url)},
    headers=${JSON.stringify(all)},
    data=${JSON.stringify(compact)},
)
print(res.status_code)  # 202 = accepted`;
  return { curl, node, python };
}

function Result({ id }: { id: string }) {
  const t = useTranslations();
  const locale = useLocale();
  const q = useLog(id);
  const l = q.data?.log;
  return (
    <div className="flex items-center gap-3 rounded-lg border border-line bg-surface-strong/40 px-3 py-2.5 text-sm">
      {l ? <Badge tone={logTone(l.status)}>{t(`logs.status.${l.status}`)}</Badge> : <Badge tone="warning">{t("logs.status.queued")}</Badge>}
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-muted">{id}</span>
      {l?.last_status_code != null && <Badge tone={statusTone(l.last_status_code)}>{l.last_status_code}</Badge>}
      {l?.latency.gateway_ms != null && <span className="text-xs text-subtle tabular-nums">{formatMs(locale, l.latency.gateway_ms)}</span>}
      {l && <span className="hidden text-xs text-subtle sm:inline">{formatRelative(locale, l.received_at)}</span>}
      <Link href={`/logs?id=${id}`} className="text-xs font-medium whitespace-nowrap text-accent hover:underline">
        {t("sandbox.openDebugger")}
      </Link>
    </div>
  );
}

/** Sandbox (design: "Sandbox / docs interativas"): send test events and copy ready-to-run snippets. */
export function SandboxPage() {
  const t = useTranslations("sandbox");
  const errorMessage = useErrorMessage();
  const params = useSearchParams();
  const endpoints = useEndpoints();
  const test = useTestEndpoint();
  const [chosen, setChosen] = useState<string | null>(params.get("endpoint"));
  const [template, setTemplate] = useState("generic");
  const [method, setMethod] = useState<Method>("POST");
  const [headers, setHeaders] = useState<KV[]>(TEMPLATES.generic.headers);
  const [body, setBody] = useState(prettyJSON(TEMPLATES.generic.body));
  const [sent, setSent] = useState<string[]>([]);
  const [lang, setLang] = useState("curl");
  const [apiBase, setApiBase] = useState("");
  useEffect(() => {
    apiBaseURL().then(setApiBase, () => undefined);
  }, []);

  const list = endpoints.data ?? [];
  const endpoint = list.find((e) => e.id === chosen) ?? list[0];
  const parsed = parseJSON(body);
  const code = useMemo(() => (endpoint ? snippets(endpoint, method, fromKV(headers), body) : null), [endpoint, method, headers, body]);

  function applyTemplate(name: string) {
    setTemplate(name);
    setHeaders(TEMPLATES[name].headers);
    setBody(prettyJSON(TEMPLATES[name].body));
  }

  function send() {
    if (!endpoint) return;
    const h = Object.fromEntries(Object.entries(fromKV(headers)).map(([k, v]) => [k, [v]]));
    const input = parsed.ok ? { method, headers: h, body: parsed.value ?? {} } : { method, headers: h, body_text: body };
    test.mutate({ id: endpoint.id, input }, { onSuccess: (r) => setSent((s) => [r.id, ...s].slice(0, 5)) });
  }

  return (
    <div className="mx-auto max-w-7xl">
      <PageHeader title={t("title")} description={t("subtitle")} />
      {endpoints.isPending ? (
        <Skeleton className="h-96 w-full" />
      ) : !endpoint ? (
        <Card>
          <EmptyState
            icon={<FlaskConical className="size-6" />}
            title={t("noEndpoints")}
            body={t("noEndpointsBody")}
            action={
              <Link href="/endpoints" className="bg-accent-gradient inline-flex h-9 items-center rounded-lg px-4 text-sm font-medium text-white">
                {t("goEndpoints")}
              </Link>
            }
          />
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <div>
                <CardTitle>{t("compose")}</CardTitle>
                <p className="mt-0.5 text-xs text-muted">{t("composeHint")}</p>
              </div>
            </CardHeader>
            <CardBody className="flex flex-col gap-4">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Select label={t("endpoint")} value={endpoint.id} onChange={(e) => setChosen(e.target.value)}>
                  {list.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.name}
                    </option>
                  ))}
                </Select>
                <Select label={t("template")} value={template} onChange={(e) => applyTemplate(e.target.value)}>
                  {Object.keys(TEMPLATES).map((k) => (
                    <option key={k} value={k}>
                      {t(`templates.${k}`)}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="flex flex-col gap-1.5">
                <span className="text-xs font-medium text-muted">{t("method")}</span>
                <Segmented size="sm" label={t("method")} value={method} onChange={setMethod} options={(["POST", "PUT", "PATCH"] as Method[]).map((m) => ({ value: m, label: m }))} />
              </div>
              <div className="flex flex-col gap-1.5">
                <span className="text-xs font-medium text-muted">{t("headers")}</span>
                <KeyValueEditor rows={headers} onChange={setHeaders} keyPlaceholder={t("headerName")} valuePlaceholder={t("headerValue")} addLabel={t("addHeader")} />
              </div>
              <Textarea label={t("body")} mono rows={12} value={body} onChange={(e) => setBody(e.target.value)} hint={parsed.ok ? undefined : t("notJson")} error={test.error ? errorMessage(test.error) : undefined} />
              {endpoint.status === "paused" && <p className="text-xs text-warning">{t("paused")}</p>}
              <div className="flex justify-end">
                <Button onClick={send} loading={test.isPending} disabled={endpoint.status === "paused"}>
                  <Send className="size-4" />
                  {t("send")}
                </Button>
              </div>
              {sent.length > 0 && (
                <div className="flex flex-col gap-2 border-t border-line pt-4">
                  <span className="text-xs font-semibold tracking-wide text-subtle uppercase">{t("results")}</span>
                  {sent.map((id) => (
                    <Result key={id} id={id} />
                  ))}
                </div>
              )}
            </CardBody>
          </Card>

          <div className="flex flex-col gap-6">
            <Card>
              <CardHeader>
                <div>
                  <CardTitle>{t("fromCode")}</CardTitle>
                  <p className="mt-0.5 text-xs text-muted">{t("fromCodeHint")}</p>
                </div>
              </CardHeader>
              <CardBody>
                {code && (
                  <Tabs
                    label={t("languages")}
                    value={lang}
                    onChange={setLang}
                    tabs={[
                      { id: "curl", label: "cURL", content: <CodeBlock value={code.curl} /> },
                      { id: "node", label: "Node.js", content: <CodeBlock value={code.node} /> },
                      { id: "python", label: "Python", content: <CodeBlock value={code.python} /> },
                    ]}
                  />
                )}
                <p className="mt-3 text-xs text-subtle">{endpoint.auth.mode === "token" ? t("authToken", { header: endpoint.auth.header, placeholder: "<token>" }) : endpoint.auth.mode === "hmac" ? t("authHmac") : t("authIp")}</p>
              </CardBody>
            </Card>
            <Card>
              <CardHeader>
                <div>
                  <CardTitle>{t("apiReference")}</CardTitle>
                  <p className="mt-0.5 text-xs text-muted">{t("apiReferenceHint")}</p>
                </div>
                <BookOpen className="size-4 text-subtle" />
              </CardHeader>
              <CardBody className="flex flex-col gap-3">
                {apiBase && <CopyField value={`${apiBase}/v1/openapi.json`} />}
                {apiBase && (
                  <a href={`${apiBase}/v1/openapi.json`} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1.5 text-xs font-medium text-accent hover:underline">
                    <ExternalLink className="size-3.5" />
                    {t("openSpec")}
                  </a>
                )}
                <Link href="/api-keys" className="text-xs text-muted hover:text-fg">
                  {t("needKey")}
                </Link>
              </CardBody>
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}
