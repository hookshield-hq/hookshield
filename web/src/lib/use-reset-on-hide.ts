"use client";

import { useLayoutEffect, useRef } from "react";

/**
 * Runs reset when the component is hidden. Next.js keeps visited routes in
 * React <Activity> (hidden, state preserved); auth forms must not keep a typed
 * password around, and menus must not reappear open. The cleanup runs
 * synchronously before the route is hidden (see Next docs: preserving UI state).
 */
export function useResetOnHide(reset: () => void) {
  const ref = useRef(reset);
  useLayoutEffect(() => {
    ref.current = reset;
  });
  useLayoutEffect(() => () => ref.current(), []);
}
