"use client";

import { NoteMarkdown } from "@/components/notes/note-markdown";
import { noteMarkdownClassName } from "@/lib/notes/markdown-style";

/** Reading and live preview share the exact Markdown components and typography. */
export function NotePreview({ vaultId, noteId, text, onLink, onTask }: {
  vaultId: string; noteId: string; text: string; onLink: (href: string) => void; onTask?: (from: number) => void;
}) {
  return <div className="czl-scroll h-full overflow-y-auto"><article className={`mx-auto max-w-4xl px-8 py-6 pb-40 ${noteMarkdownClassName}`}>
    <NoteMarkdown vaultId={vaultId} noteId={noteId} text={text} onLink={onLink} onTask={onTask} />
  </article></div>;
}
