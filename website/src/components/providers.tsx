"use client";

import { ThemeProvider } from "next-themes";

/** Dark by default (design principle), light one click away; remembered per browser. */
export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="dark" enableSystem={false} disableTransitionOnChange>
      {children}
    </ThemeProvider>
  );
}
