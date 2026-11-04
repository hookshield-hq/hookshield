import { RootShell } from "@/components/root-shell";

// Root layout of the English pages: the landing page (/), the docs and the
// legal pages. The other languages have their own root layout in [locale].
export default function EnglishLayout({ children }: { children: React.ReactNode }) {
  return <RootShell locale="en">{children}</RootShell>;
}
