import Link from "next/link";
import type { Metadata } from "next";
import { Footer } from "@/components/footer";
import { Header } from "@/components/header";
import { RootShell } from "@/components/root-shell";
import { getT } from "@/i18n";
import { baseMetadata } from "@/lib/metadata";

const t = getT("en");

export const metadata: Metadata = { ...baseMetadata, title: `${t("notFound.title")} · HookShield`, robots: { index: false } };

/** 404 for every unknown URL (static hosts serve out/404.html). */
export default function GlobalNotFound() {
  return (
    <RootShell locale="en">
      <Header locale="en" current="home" />
      <main id="content" className="mx-auto flex min-h-[60dvh] max-w-xl flex-col items-center justify-center px-5 py-24 text-center">
        <p className="text-accent-gradient font-mono text-6xl font-semibold">404</p>
        <h1 className="mt-4 text-2xl font-semibold">{t("notFound.title")}</h1>
        <p className="mt-2 text-muted">{t("notFound.body")}</p>
        <div className="mt-8 flex gap-3">
          <Link href="/" className="bg-accent-gradient rounded-xl px-4 py-2.5 text-sm font-medium text-white">
            {t("notFound.home")}
          </Link>
          <Link href="/docs/" className="glass rounded-xl px-4 py-2.5 text-sm font-medium">
            {t("notFound.docs")}
          </Link>
        </div>
      </main>
      <Footer locale="en" />
    </RootShell>
  );
}
