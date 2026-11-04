import { Suspense } from "react";
import { AppShell, ShellSkeleton } from "@/components/shell/app-shell";

export default function AppLayout({ children }: LayoutProps<"/[locale]">) {
  // The shell reads the URL (active nav item, sign-in redirect), which is
  // request data on dynamic routes such as /endpoints/[id].
  return (
    <Suspense fallback={<ShellSkeleton />}>
      <AppShell>{children}</AppShell>
    </Suspense>
  );
}
