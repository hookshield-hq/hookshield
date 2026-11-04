import type { HTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export type Tone = "success" | "warning" | "danger" | "info" | "neutral";

const tones: Record<Tone, string> = {
  success: "bg-success-bg text-success",
  warning: "bg-warning-bg text-warning",
  danger: "bg-danger-bg text-danger",
  info: "bg-info-bg text-info",
  neutral: "bg-hover text-muted",
};

export function Badge({ tone = "neutral", className, ...rest }: HTMLAttributes<HTMLSpanElement> & { tone?: Tone }) {
  return (
    <span
      className={cn("inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-medium tabular-nums", tones[tone], className)}
      {...rest}
    />
  );
}

/** Tone for an HTTP status: 2xx success, 4xx/5xx danger, others neutral. */
export function statusTone(code?: number | null): Tone {
  if (!code) return "neutral";
  if (code >= 200 && code < 300) return "success";
  if (code >= 400) return "danger";
  return "warning";
}

/** Tone for a webhook log status. */
export function logTone(status: string): Tone {
  switch (status) {
    case "delivered":
      return "success";
    case "retrying":
    case "queued":
    case "processing":
      return "warning";
    case "failed":
    case "dead":
      return "danger";
    default:
      return "neutral";
  }
}
