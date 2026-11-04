"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, data, type components } from "./api/client";
import { ApiError } from "./api/errors";

export type User = components["schemas"]["User"];

/** The signed-in user. A 401 means "signed out" (data null), not an error. */
export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: async (): Promise<User | null> => {
      try {
        const c = await api();
        return data(await c.GET("/v1/me")).user;
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null;
        throw err;
      }
    },
    staleTime: 60_000,
  });
}

export function useProviders() {
  return useQuery({
    queryKey: ["auth-providers"],
    queryFn: async () => data(await (await api()).GET("/v1/auth/providers")),
    staleTime: Infinity,
  });
}

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { email: string; password: string }) =>
      data(await (await api()).POST("/v1/auth/login", { body })).user,
    onSuccess: (user) => qc.setQueryData(["me"], user),
  });
}

export function useRegister() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { email: string; password: string; name?: string; locale?: "en" | "pt-BR" | "es" }) =>
      data(await (await api()).POST("/v1/auth/register", { body })).user,
    onSuccess: (user) => qc.setQueryData(["me"], user),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      try {
        await (await api()).POST("/v1/auth/logout");
      } catch (err) {
        // 401: the session is already gone, so we are signed out anyway.
        if (!(err instanceof ApiError && err.status === 401)) throw err;
      }
    },
    // Only once the server ended the session: on a failure the HttpOnly cookie
    // is still valid, so the user stays signed in (the caller shows an error).
    onSuccess: () => {
      qc.setQueryData(["me"], null);
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" && q.queryKey[0] !== "auth-providers" });
    },
  });
}

/** Saves a preference (language, theme, name) on the account. */
export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: { name?: string; locale?: "en" | "pt-BR" | "es"; theme_preference?: "dark" | "light" | "system" }) =>
      data(await (await api()).PATCH("/v1/me", { body })).user,
    onSuccess: (user) => qc.setQueryData(["me"], user),
  });
}

/** Builds the Google sign-in URL (a full-page navigation, not a fetch). */
export function googleStartURL(apiBase: string, redirect: string): string {
  return `${apiBase}/v1/auth/google/start?redirect=${encodeURIComponent(redirect)}`;
}
