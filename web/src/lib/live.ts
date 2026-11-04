"use client";

import { useEffect, useRef, useState } from "react";
import { apiBaseURL } from "./api/config";

/** An event of the live feed (GET /v1/live). Events never carry payloads. */
export type LiveEvent = {
  type: "log.created" | "log.updated";
  log_id: string;
  endpoint_id: string;
  status: string;
  method?: string;
  event_type?: string | null;
  latency_ms?: number;
  attempt?: number;
  status_code?: number | null;
  at: string;
};

export type LiveState = "connecting" | "open" | "closed";

/**
 * Subscribes to the account's live feed over Server-Sent Events. The browser
 * reconnects by itself; onReconnect runs after a reconnection so screens can
 * reload what they may have missed (delivery is best-effort).
 */
export function useLiveEvents(onEvent: (e: LiveEvent) => void, opts: { endpointId?: string; enabled?: boolean; onReconnect?: () => void } = {}): LiveState {
  const { endpointId, enabled = true } = opts;
  const [state, setState] = useState<LiveState>("connecting");
  const handler = useRef(onEvent);
  const reconnect = useRef(opts.onReconnect);
  useEffect(() => {
    handler.current = onEvent;
    reconnect.current = opts.onReconnect;
  });

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") return;
    let source: EventSource | null = null;
    let cancelled = false;
    let opened = false;
    void apiBaseURL().then((base) => {
      if (cancelled) return;
      const url = `${base}/v1/live${endpointId ? `?endpoint_id=${encodeURIComponent(endpointId)}` : ""}`;
      source = new EventSource(url, { withCredentials: true });
      source.onopen = () => {
        setState("open");
        if (opened) reconnect.current?.();
        opened = true;
      };
      source.onerror = () => setState(source?.readyState === EventSource.CLOSED ? "closed" : "connecting");
      const listen = (e: MessageEvent<string>) => {
        try {
          handler.current(JSON.parse(e.data) as LiveEvent);
        } catch {
          // ignore malformed events
        }
      };
      source.addEventListener("log.created", listen);
      source.addEventListener("log.updated", listen);
    });
    return () => {
      cancelled = true;
      source?.close();
    };
  }, [endpointId, enabled]);

  return enabled ? state : "closed";
}
