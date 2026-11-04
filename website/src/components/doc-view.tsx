import { ArrowLeft, ArrowRight, Pencil } from "lucide-react";
import { docHref, docsNav, editUrl, neighbours, type RenderedDoc } from "@/lib/docs";
import { getT } from "@/i18n";
import { Footer } from "./footer";
import { Header } from "./header";

/** A docs page: sidebar, rendered Markdown, table of contents, previous/next. */
export function DocView({ doc }: { doc: RenderedDoc }) {
  const t = getT("en");
  const { prev, next } = neighbours(doc.page.slug);
  return (
    <>
      <Header locale="en" current="docs" />
      <div className="mx-auto flex max-w-7xl gap-10 px-5">
        <nav aria-label={t("docs.toc")} className="sticky top-16 hidden h-[calc(100dvh-4rem)] w-56 shrink-0 overflow-y-auto py-10 lg:block">
          {docsNav.map((section) => (
            <div key={section.id} className="mb-7">
              <h2 className="px-3 text-xs font-semibold uppercase tracking-wider text-subtle">{t(`docs.sections.${section.id}`)}</h2>
              <ul className="mt-2 space-y-0.5 text-sm">
                {section.pages.map((p) => {
                  const active = p.slug === doc.page.slug;
                  return (
                    <li key={p.slug}>
                      <a
                        href={docHref(p.slug)}
                        aria-current={active ? "page" : undefined}
                        className={`block rounded-lg px-3 py-1.5 transition ${active ? "bg-hover font-medium text-fg" : "text-muted hover:bg-hover hover:text-fg"}`}
                      >
                        {p.label}
                      </a>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </nav>

        <main id="content" className="min-w-0 flex-1 py-10">
          {/* Small screens: the sidebar becomes a select-like disclosure. */}
          <details className="mb-8 rounded-xl border border-line lg:hidden">
            <summary className="cursor-pointer px-4 py-3 text-sm font-medium">{t("docs.title")}</summary>
            <ul className="border-t border-line p-2 text-sm">
              {docsNav.flatMap((s) => s.pages).map((p) => (
                <li key={p.slug}>
                  <a href={docHref(p.slug)} className="block rounded-lg px-3 py-1.5 text-muted hover:bg-hover hover:text-fg">
                    {p.label}
                  </a>
                </li>
              ))}
            </ul>
          </details>

          <article>
            <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">{doc.title}</h1>
            <div className="prose mt-6" dangerouslySetInnerHTML={{ __html: doc.html }} />
          </article>

          <div className="mt-14 flex flex-wrap items-center justify-between gap-4 border-t border-line pt-6 text-sm">
            <a href={editUrl(doc.page)} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1.5 text-muted hover:text-fg">
              <Pencil className="size-4" aria-hidden />
              {t("docs.edit")}
            </a>
          </div>
          <nav className="mt-6 grid gap-4 sm:grid-cols-2" aria-label={`${t("docs.previous")} / ${t("docs.next")}`}>
            {prev ? (
              <a href={docHref(prev.slug)} className="rounded-xl border border-line p-4 transition hover:border-accent">
                <span className="inline-flex items-center gap-1 text-xs text-subtle">
                  <ArrowLeft className="size-3.5" aria-hidden />
                  {t("docs.previous")}
                </span>
                <span className="mt-1 block font-medium">{prev.label}</span>
              </a>
            ) : (
              <span />
            )}
            {next && (
              <a href={docHref(next.slug)} className="rounded-xl border border-line p-4 text-right transition hover:border-accent">
                <span className="inline-flex items-center gap-1 text-xs text-subtle">
                  {t("docs.next")}
                  <ArrowRight className="size-3.5" aria-hidden />
                </span>
                <span className="mt-1 block font-medium">{next.label}</span>
              </a>
            )}
          </nav>
        </main>

        {doc.toc.length > 0 && (
          <nav aria-label={t("docs.onThisPage")} className="sticky top-16 hidden h-[calc(100dvh-4rem)] w-52 shrink-0 overflow-y-auto py-10 xl:block">
            <h2 className="text-xs font-semibold uppercase tracking-wider text-subtle">{t("docs.onThisPage")}</h2>
            <ul className="mt-3 space-y-1.5 text-sm">
              {doc.toc.map((item) => (
                <li key={item.id} className={item.depth === 3 ? "pl-3" : ""}>
                  <a href={`#${item.id}`} className="block text-muted hover:text-fg">
                    {item.text}
                  </a>
                </li>
              ))}
            </ul>
          </nav>
        )}
      </div>
      <Footer locale="en" />
    </>
  );
}
