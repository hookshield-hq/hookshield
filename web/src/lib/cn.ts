/** Joins class names, skipping falsy values. */
export function cn(...parts: unknown[]): string {
  return parts.filter((p): p is string => typeof p === "string" && p !== "").join(" ");
}
