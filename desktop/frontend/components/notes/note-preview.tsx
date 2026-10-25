"use client";

import { useLayoutEffect, useRef } from "react";
import { noteSession, readingViewport, restoreReadingViewport } from "@/lib/notes/view-session";
import { NoteMarkdown } from "@/components/notes/note-markdown";
import { noteMarkdownClassName } from "@/lib/notes/markdown-style";

/** Reading and live preview share the exact Markdown components and typography. */
export function NotePreview({ vaultId, noteId, text, onLink, onTask }: {
  vaultId: string; noteId: string; text: string; onLink: (href: string) => void; onTask?: (from: number) => void;
}) {
  const root = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const host = root.current;
    if (!host) return;
    const session = noteSession(vaultId, noteId);
    restoreReadingViewport(host, session.viewport);
    return () => { session.viewport = readingViewport(host); };
  }, [vaultId, noteId]);
  return <div ref={root} className="czl-scroll h-full overflow-y-auto"><article className={`mx-auto max-w-4xl px-8 py-6 pb-40 ${noteMarkdownClassName}`}>
    <NoteMarkdown vaultId={vaultId} noteId={noteId} text={text} onLink={onLink} onTask={onTask} />
  </article></div>;
}
