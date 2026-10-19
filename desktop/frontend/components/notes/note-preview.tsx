"use client";

import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { toast } from "sonner";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { frontmatter } from "@/lib/notes/markdown";
import { readingMarkdown } from "@/lib/notes/reading-markdown";
import { NoteImagePreview } from "@/components/notes/note-image-preview";

/** Markdown preview never executes HTML, remote images or embedded active content. */
export function NotePreview({ vaultId, noteId, text, onLink }: {
  vaultId: string; noteId: string; text: string; onLink: (href: string) => void;
}) {
  useI18n();
  // Imported wiki embeds are transformed for rendering only; original bytes remain unchanged.
  const metadata = frontmatter(text);
  const body = metadata ? text.slice(metadata.to) : text;
  const rendered = readingMarkdown(body);
  return <div className="czl-scroll h-full overflow-y-auto"><article className="mx-auto max-w-4xl px-8 py-6 pb-40 text-base leading-8 [&_h1]:my-6 [&_h1]:text-3xl [&_h1]:font-bold [&_h2]:my-5 [&_h2]:text-2xl [&_h2]:font-semibold [&_h3]:my-4 [&_h3]:text-xl [&_h3]:font-semibold [&_p]:my-3 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:list-decimal [&_ol]:pl-6 [&_blockquote]:border-l-2 [&_blockquote]:pl-4 [&_blockquote]:text-muted-foreground [&_a]:text-primary [&_a]:underline [&_table]:w-full [&_td]:border [&_td]:px-3 [&_th]:border [&_th]:px-3 [&_pre]:overflow-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-4 [&_code]:font-mono [&_code]:text-sm">
    {metadata && <details className="text-muted-foreground mb-6 text-sm"><summary className="cursor-pointer">{t("属性")} · {metadata.rows.length}</summary>{metadata.error ? <pre>{metadata.raw}</pre> : <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">{metadata.rows.map(([key, value]) => <div key={key} className="contents"><dt>{key}</dt><dd className="min-w-0 break-words whitespace-pre-wrap">{value}</dd></div>)}</dl>}</details>}
    <Markdown skipHtml remarkPlugins={[remarkGfm]} components={{
      a: ({ href, children }) => <a href={href} onClick={(event) => { event.preventDefault(); if (href) onLink(href); }}>{children}</a>,
      img: ({ src, alt }) => {
        if (typeof src !== "string") return null;
        return <NoteImagePreview key={src} src={src} alt={alt ?? ""} vaultId={vaultId} noteId={noteId} />;
      },
      pre: ({ children }) => <div className="relative"><button type="button" className="text-muted-foreground absolute top-2 right-3 text-sm" onClick={(event) => { const code = event.currentTarget.parentElement?.querySelector("code")?.textContent ?? ""; void navigator.clipboard.writeText(code).then(() => toast.success(t("已复制"))); }}>{t("复制")}</button><pre className="!pt-10 !leading-relaxed whitespace-pre">{children}</pre></div>,
    }}>{rendered}</Markdown>
  </article></div>;
}
