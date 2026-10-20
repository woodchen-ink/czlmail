"use client";

import type { MouseEvent } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { frontmatter } from "@/lib/notes/markdown";
import { prepareReadingMarkdown } from "@/lib/notes/reading-markdown";
import { remarkNoteBreaks, rehypeNoteAnchors, rehypeNoteRange, type MarkdownRange } from "@/lib/notes/render-markdown";
import { NoteImagePreview } from "@/components/notes/note-image-preview";

/** Both reading and live blocks use this typography; no second set of Markdown styles. */
export const noteMarkdownClassName = "text-base leading-8 whitespace-normal [&_h1]:my-6 [&_h1]:text-3xl [&_h1]:font-bold [&_h2]:my-5 [&_h2]:text-2xl [&_h2]:font-semibold [&_h3]:my-4 [&_h3]:text-xl [&_h3]:font-semibold [&_h4]:my-3 [&_h4]:font-semibold [&_h5]:my-3 [&_h5]:font-semibold [&_h6]:my-3 [&_h6]:font-semibold [&_p]:my-3 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:list-decimal [&_ol]:pl-6 [&_blockquote]:border-border [&_blockquote]:border-l-2 [&_blockquote]:pl-4 [&_blockquote]:text-muted-foreground [&_a]:text-primary [&_a]:underline [&_table]:w-full [&_table]:border-collapse [&_td]:border-border [&_td]:border [&_td]:px-3 [&_td]:py-1 [&_th]:border-border [&_th]:border [&_th]:px-3 [&_th]:py-1 [&_th]:bg-muted [&_pre]:overflow-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-4 [&_code]:font-mono [&_code]:text-sm [&_hr]:my-4 [&_hr]:border-border";

/** Shared, safe Markdown renderer; live preview limits output without losing document references. */
export function NoteMarkdown({ vaultId, noteId, text, onLink, range }: {
  vaultId: string; noteId: string; text: string; onLink: (href: string) => void; range?: MarkdownRange;
}) {
  useI18n();
  const metadata = frontmatter(text);
  const offset = metadata?.to ?? 0;
  const body = text.slice(offset);
  const prepared = prepareReadingMarkdown(body);
  const rendered = prepared.text;
  const renderedRange = range ? { from: prepared.toRendered(Math.max(0, range.from - offset)), to: prepared.toRendered(Math.max(0, range.to - offset)), footnotesOnly: range.footnotesOnly } : undefined;
  /** Virtualized live blocks first scroll to source, then locate the mounted fragment. */
  function handleLink(event: MouseEvent<HTMLAnchorElement>, href: string, source?: number) {
    event.preventDefault();
    if (!href.startsWith("#")) { onLink(href); return; }
    const id = href.slice(1);
    const position = href.startsWith("#user-content-fn-") ? text.length : source === undefined ? undefined : offset + prepared.toSource(source);
    const jump = new CustomEvent("czl-note-anchor", { bubbles: true, cancelable: true, detail: { id, position } });
    if (!event.currentTarget.dispatchEvent(jump)) return;
    try { (document.getElementById(id) ?? document.getElementById(decodeURIComponent(id)))?.scrollIntoView({ block: "nearest" }); } catch { /* Ignore malformed fragments. */ }
  }
  return <>
    {metadata && (!range || range.from < metadata.to) && <details className="text-muted-foreground mb-6 text-sm"><summary className="cursor-pointer">{t("属性")} · {metadata.rows.length}</summary>{metadata.error ? <pre>{metadata.raw}</pre> : <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">{metadata.rows.map(([key, value]) => <div key={key} className="contents"><dt>{key}</dt><dd className="min-w-0 break-words whitespace-pre-wrap">{value}</dd></div>)}</dl>}</details>}
    <Markdown skipHtml remarkPlugins={[remarkGfm, remarkNoteBreaks]} rehypePlugins={[rehypeNoteAnchors, [rehypeNoteRange, renderedRange]]} components={{
      a: ({ href, children, node, id, className, "aria-label": label, "aria-describedby": describedBy }) => <a href={href} id={id} className={className} aria-label={label} aria-describedby={describedBy} data-footnote-ref={node?.properties.dataFootnoteRef === undefined ? undefined : ""} data-footnote-backref={node?.properties.dataFootnoteBackref === undefined ? undefined : ""} data-note-link={href?.startsWith("#") ? undefined : href} onClick={(event) => handleLink(event, href ?? "", typeof node?.properties.dataNoteAnchorPosition === "number" ? node.properties.dataNoteAnchorPosition : undefined)}>{children}</a>,
      img: ({ src, alt }) => typeof src === "string" ? <NoteImagePreview key={src} src={src} alt={alt ?? ""} vaultId={vaultId} noteId={noteId} /> : null,
      table: ({ children }) => <div className="my-3 overflow-x-auto"><table>{children}</table></div>,
      pre: ({ children }) => <div className="relative"><button type="button" className="text-muted-foreground absolute top-2 right-3 text-sm" onClick={(event) => { const code = event.currentTarget.parentElement?.querySelector("code")?.textContent ?? ""; void navigator.clipboard.writeText(code).then(() => toast.success(t("已复制"))).catch(() => toast.error(t("复制失败，请手动选择代码"))); }}>{t("复制")}</button><pre className="!pt-10 !leading-relaxed whitespace-pre">{children}</pre></div>,
    }}>{rendered}</Markdown>
  </>;
}
