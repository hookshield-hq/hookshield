"use client";

import { Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";

export function ThemeToggle({ label }: { label: string }) {
  const { resolvedTheme, setTheme } = useTheme();
  return (
    <button
      type="button"
      onClick={() => setTheme(resolvedTheme === "light" ? "dark" : "light")}
      aria-label={label}
      title={label}
      className="inline-flex size-9 items-center justify-center rounded-lg text-muted transition hover:bg-hover hover:text-fg"
    >
      {/* Both icons are rendered; CSS shows the right one, so there is no flash before hydration. */}
      <Sun className="hidden size-4 dark:block" aria-hidden />
      <Moon className="size-4 dark:hidden" aria-hidden />
    </button>
  );
}
