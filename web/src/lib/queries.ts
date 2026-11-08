"use client";

// Data hooks for every dashboard screen. Each one wraps the typed API client
// (src/lib/api/client.ts) in TanStack Query, so screens share caches and
// mutations refresh exactly what they change.

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, data, type components } from "./api/client";
import { ApiError } from "./api/errors";

type Schemas = components["schemas"];
export type Endpoint = Schemas["Endpoint"];
export type EndpointInput = Schemas["EndpointInput"];
export type EndpointResponse = Schemas["EndpointResponse"];
export type TransformRules = Schemas["TransformRules"];
export type BodyRule = Schemas["BodyRule"];
export type HeaderRule = Schemas["HeaderRule"];
export type Retry = Schemas["Retry"];
export type LogSummary = Schemas["LogSummary"];
export type LogDetail = Schemas["LogDetail"];
export type Attempt = Schemas["Attempt"];
export type LogStatus = LogSummary["status"];
export type Secret = Schemas["Secret"];
export type AlertChannel = Schemas["AlertChannel"];
export type ChannelTarget = Schemas["ChannelTarget"];
export type APIKey = Schemas["APIKey"];
export type MetricsRange = "1h" | "24h" | "7d" | "30d";

export const LOG_STATUSES: LogStatus[] = ["queued", "processing", "delivered", "retrying", "failed", "dead"];

// ── endpoints ─────────────────────────────────────────────

export function useEndpoints() {
  return useQuery({
    queryKey: ["endpoints"],
    queryFn: async () => data(await (await api()).GET("/v1/endpoints")).endpoints,
  });
}

export function useEndpoint(id: string | null | undefined) {
  return useQuery({
    queryKey: ["endpoint", id],
    enabled: !!id,
    queryFn: async () => data(await (await api()).GET("/v1/endpoints/{id}", { params: { path: { id: id! } } })),
  });
}

function useEndpointMutation<V>(fn: (v: V) => Promise<EndpointResponse>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: (res) => {
      qc.setQueryData(["endpoint", res.endpoint.id], { endpoint: res.endpoint, warnings: res.warnings ?? [] });
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      void qc.invalidateQueries({ queryKey: ["metrics", "endpoints"] });
    },
  });
}

export function useCreateEndpoint() {
  return useEndpointMutation(async (body: EndpointInput) => data(await (await api()).POST("/v1/endpoints", { body })));
}

export function useUpdateEndpoint(id: string) {
  return useEndpointMutation(async (body: EndpointInput) =>
    data(await (await api()).PATCH("/v1/endpoints/{id}", { params: { path: { id } }, body })),
  );
}

export function useRotateSecret(id: string) {
  return useEndpointMutation(async () =>
    data(await (await api()).POST("/v1/endpoints/{id}/rotate-secret", { params: { path: { id } } })),
  );
}

export function useDeleteEndpoint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      await (await api()).DELETE("/v1/endpoints/{id}", { params: { path: { id } } });
      return id;
    },
    // The detail query is left alone: removing it while the page is still
    // mounted would refetch a deleted endpoint (404).
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["endpoints"] });
      void qc.invalidateQueries({ queryKey: ["logs"] });
    },
  });
}

export type TestEventInput = {
  method?: "POST" | "PUT" | "PATCH";
  headers?: Record<string, string[]>;
  body?: unknown;
  body_text?: string;
};

/** Sends a test webhook through the whole pipeline (sandbox, onboarding). */
export function useTestEndpoint() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, input }: { id: string; input?: TestEventInput }) =>
      data(await (await api()).POST("/v1/endpoints/{id}/test", { params: { path: { id } }, body: input })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["logs"] }),
  });
}

// ── logs ──────────────────────────────────────────────────

export type LogFilters = {
  endpoint_id?: string;
  status?: LogStatus[];
  event_type?: string;
  q?: string;
  from?: string;
  to?: string;
};

export function useLogs(filters: LogFilters, limit = 50) {
  return useInfiniteQuery({
    queryKey: ["logs", filters, limit],
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }) => {
      const query = {
        endpoint_id: filters.endpoint_id || undefined,
        status: filters.status?.length ? filters.status.join(",") : undefined,
        event_type: filters.event_type || undefined,
        q: filters.q || undefined,
        from: filters.from || undefined,
        to: filters.to || undefined,
        limit,
        cursor: pageParam,
      };
      return data(await (await api()).GET("/v1/logs", { params: { query } }));
    },
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
}

export function useLog(id: string | null | undefined) {
  return useQuery({
    queryKey: ["log", id],
    enabled: !!id,
    queryFn: async () => data(await (await api()).GET("/v1/logs/{id}", { params: { path: { id: id! } } })),
    // A webhook that was just sent or replayed is stored a moment after the
    // API accepts it: keep asking for a few seconds before showing "not found".
    retry: (count, err) => (err instanceof ApiError && err.status === 404 ? count < 12 : !(err instanceof ApiError && err.status < 500) && count < 2),
    retryDelay: (count) => Math.min(250 * 2 ** count, 1000),
    // Refresh while the webhook is still moving through the pipeline.
    refetchInterval: (q) => {
      const s = q.state.data?.log.status;
      return s === "queued" || s === "processing" || s === "retrying" ? 2_000 : false;
    },
  });
}

export type ReplayInput = { body?: unknown; body_text?: string; headers?: Record<string, string[]> };

export function useReplay() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, input }: { id: string; input?: ReplayInput }) =>
      data(await (await api()).POST("/v1/logs/{id}/replay", { params: { path: { id } }, body: input })),
    onSuccess: (_, { id }) => {
      void qc.invalidateQueries({ queryKey: ["logs"] });
      void qc.invalidateQueries({ queryKey: ["log", id] });
    },
  });
}

// ── metrics ───────────────────────────────────────────────

export function useMetricsSummary(range: MetricsRange, endpointId?: string) {
  return useQuery({
    queryKey: ["metrics", "summary", range, endpointId ?? null],
    queryFn: async () =>
      data(await (await api()).GET("/v1/metrics/summary", { params: { query: { range, endpoint_id: endpointId } } })),
    refetchInterval: 30_000,
  });
}

export function useMetricsSeries(range: MetricsRange, endpointId?: string) {
  return useQuery({
    queryKey: ["metrics", "series", range, endpointId ?? null],
    queryFn: async () =>
      data(await (await api()).GET("/v1/metrics/series", { params: { query: { range, endpoint_id: endpointId } } })),
    refetchInterval: 30_000,
  });
}

export function useEndpointMetrics(range: MetricsRange = "24h") {
  return useQuery({
    queryKey: ["metrics", "endpoints", range],
    queryFn: async () => data(await (await api()).GET("/v1/metrics/endpoints", { params: { query: { range } } })),
    refetchInterval: 60_000,
  });
}

// ── secrets ───────────────────────────────────────────────

export function useSecrets() {
  return useQuery({
    queryKey: ["secrets"],
    queryFn: async () => data(await (await api()).GET("/v1/secrets")).secrets,
  });
}

export function usePutSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ name, value }: { name: string; value: string }) =>
      data(await (await api()).PUT("/v1/secrets/{name}", { params: { path: { name } }, body: { value } })).secret,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["secrets"] });
      void qc.invalidateQueries({ queryKey: ["endpoint"] }); // warnings about missing secrets
    },
  });
}

export function useDeleteSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (name: string) => {
      await (await api()).DELETE("/v1/secrets/{name}", { params: { path: { name } } });
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["secrets"] });
      void qc.invalidateQueries({ queryKey: ["endpoint"] });
    },
  });
}

// ── alert channels ────────────────────────────────────────

export function useAlertChannels() {
  return useQuery({
    queryKey: ["alert-channels"],
    queryFn: async () => data(await (await api()).GET("/v1/alert-channels")).alert_channels,
  });
}

export type AlertChannelInput = {
  type: AlertChannel["type"];
  target: ChannelTarget;
  endpoint_id?: string | null;
  enabled?: boolean;
};

export function useCreateAlertChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: AlertChannelInput) => data(await (await api()).POST("/v1/alert-channels", { body })).alert_channel,
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["alert-channels"] }),
  });
}

export function useUpdateAlertChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: { id: string; body: { enabled?: boolean; endpoint_id?: string | null; target?: ChannelTarget } }) =>
      data(await (await api()).PATCH("/v1/alert-channels/{id}", { params: { path: { id } }, body })).alert_channel,
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["alert-channels"] }),
  });
}

export function useDeleteAlertChannel() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      await (await api()).DELETE("/v1/alert-channels/{id}", { params: { path: { id } } });
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["alert-channels"] }),
  });
}

// ── API keys ──────────────────────────────────────────────

export function useApiKeys() {
  return useQuery({
    queryKey: ["api-keys"],
    queryFn: async () => data(await (await api()).GET("/v1/api-keys")).api_keys,
  });
}

export function useCreateApiKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { name: string; scope: APIKey["scope"]; expires_in_days?: number }) =>
      data(await (await api()).POST("/v1/api-keys", { body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["api-keys"] }),
  });
}

export function useRevokeApiKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      await (await api()).DELETE("/v1/api-keys/{id}", { params: { path: { id } } });
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["api-keys"] }),
  });
}

// ── transform preview ─────────────────────────────────────

export type PreviewRequest = { headers?: Record<string, string | string[]>; body?: unknown; body_text?: string };

export function usePreviewTransform() {
  return useMutation({
    mutationFn: async ({ rules, request }: { rules: TransformRules | null; request: PreviewRequest }) =>
      data(await (await api()).POST("/v1/transform/preview", { body: { rules, request } })),
  });
}

// ── account ───────────────────────────────────────────────

export function useChangePassword() {
  return useMutation({
    mutationFn: async (body: { current_password: string; new_password: string }) => {
      await (await api()).POST("/v1/auth/change-password", { body });
    },
  });
}

export function useResendVerification() {
  return useMutation({
    mutationFn: async () => {
      await (await api()).POST("/v1/auth/resend-verification");
    },
  });
}

// ── features & billing (HookShield Cloud) ─────────────────

/** Optional features of this server; billing exists only on HookShield Cloud. */
export function useFeatures() {
  return useQuery({
    queryKey: ["features"],
    queryFn: async () => data(await (await api()).GET("/v1/features")),
    staleTime: Infinity,
  });
}

export type BillingOverview = Schemas["BillingOverview"];
export type BillingOffer = Schemas["BillingOffer"];
export type BillingSubscription = Schemas["BillingSubscription"];

export function useBilling(opts: { enabled?: boolean; refetchInterval?: number | false | ((data: BillingOverview | undefined) => number | false) } = {}) {
  const interval = opts.refetchInterval;
  return useQuery({
    queryKey: ["billing"],
    enabled: opts.enabled ?? true,
    refetchInterval: typeof interval === "function" ? (q) => interval(q.state.data) : interval,
    queryFn: async () => data(await (await api()).GET("/v1/billing")),
  });
}

export type BillingInterval = "month" | "year";
export type CheckoutInput = { plan: "pro" | "business"; interval: BillingInterval };

export function useCheckout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ plan, interval }: CheckoutInput) => data(await (await api()).POST("/v1/billing/checkout", { body: { plan, interval } })),
    onSuccess: (r) => {
      if (r.subscription) {
        void qc.invalidateQueries({ queryKey: ["billing"] });
        void qc.invalidateQueries({ queryKey: ["me"] });
      }
    },
  });
}

export function usePortal() {
  return useMutation({
    mutationFn: async () => data(await (await api()).POST("/v1/billing/portal")).url,
  });
}
