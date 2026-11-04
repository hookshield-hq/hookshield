"use client";

import { useTranslations } from "next-intl";
import { ApiError } from "./api/errors";

/**
 * Returns a function that turns any error into a translated sentence.
 * API codes map to the "errors" namespace ("endpoint.slug_taken" →
 * errors.endpoint.slug_taken); unknown codes fall back to errors.unknown.
 */
export function useErrorMessage() {
  const t = useTranslations("errors");
  return (err: unknown, values?: Record<string, string | number>): string => {
    const code = err instanceof ApiError ? err.code : "unknown";
    return t.has(code) ? t(code, values) : t("unknown");
  };
}

/**
 * Splits an API error between form fields: fieldError(name) returns the
 * translated message when the error's code belongs to that field (per
 * fieldOf), and formError is the message for everything else.
 */
export function useFieldErrors(err: unknown, fieldOf: (code: string) => string | undefined) {
  const message = useErrorMessage();
  const code = err instanceof ApiError ? err.code : undefined;
  const details = err instanceof ApiError && err.details && typeof err.details === "object" && !Array.isArray(err.details) ? (err.details as Record<string, string | number>) : undefined;
  const field = code ? fieldOf(code) : undefined;
  return {
    fieldError: (name: string) => (err && field === name ? message(err, details) : undefined),
    formError: err && !field ? message(err, details) : undefined,
  };
}
