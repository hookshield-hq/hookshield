import { Activity, Bell, CreditCard, FlaskConical, KeyRound, LayoutDashboard, Lock, Settings, Shuffle, Webhook, type LucideIcon } from "lucide-react";

/** Public documentation (the website renders docs/*.md). */
export const DOCS_URL = "https://hookshield.dev/docs/";

export type NavItem = { href: string; key: string; icon: LucideIcon; feature?: "billing" };

// Labels come from messages/*.json → nav.<key>.
export const navItems: NavItem[] = [
  { href: "/", key: "dashboard", icon: LayoutDashboard },
  { href: "/endpoints", key: "endpoints", icon: Webhook },
  { href: "/logs", key: "logs", icon: Activity },
  { href: "/transform", key: "transform", icon: Shuffle },
  { href: "/sandbox", key: "sandbox", icon: FlaskConical },
  { href: "/alerts", key: "alerts", icon: Bell },
  { href: "/secrets", key: "secrets", icon: Lock },
  { href: "/api-keys", key: "apiKeys", icon: KeyRound },
  { href: "/billing", key: "billing", icon: CreditCard, feature: "billing" },
  { href: "/settings", key: "settings", icon: Settings },
];

/** Items this server offers (billing only exists on HookShield Cloud). */
export function visibleNav(features: { billing: boolean } | undefined): NavItem[] {
  return navItems.filter((i) => !i.feature || features?.[i.feature]);
}

export function isActive(pathname: string, href: string): boolean {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(href + "/");
}
