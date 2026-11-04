import { createNavigation } from "next-intl/navigation";
import { routing } from "./routing";

// Locale-aware wrappers around Next.js navigation APIs. Use these instead of
// next/link and next/navigation so every link keeps the current language.
export const { Link, redirect, usePathname, useRouter, getPathname } = createNavigation(routing);
