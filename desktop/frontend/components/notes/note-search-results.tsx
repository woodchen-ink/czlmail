"use client";

import { FileText, Folder, Paperclip } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useI18n } from "@/components/locale-provider";
import { cn } from "@/lib/utils";
import type { NoteEntry } from "@/lib/notes/api";
import { t } from "@/lib/i18n";

/** Search hits and trash entries: name, folder path and the matching line, newest first. */
export function NoteSearchResults({ entries, query, trash, selectedId, disabled, onOpen, onRestore }: {
  entries: NoteEntry[]; query: string; trash: boolean; selectedId: string; disabled: boolean;
  onOpen: (entry: NoteEntry) => void; onRestore: (entry: NoteEntry) => void;
}) {
  useI18n();
  if (!entries.length) return <p className="text-muted-foreground px-2 py-6 text-center text-sm">{trash ? t("回收站是空的") : t("没有找到匹配的笔记")}</p>;
  return <div className="flex flex-col gap-0.5">
    {entries.map((entry) => {
      const Icon = entry.directory ? Folder : entry.attachment ? Paperclip : FileText;
      return <div key={entry.id} className={cn("hover:bg-secondary flex items-start gap-1 rounded-md", selectedId === entry.id && "bg-muted")}>
        <button type="button" disabled={disabled || entry.directory} onClick={() => onOpen(entry)} className="flex min-w-0 flex-1 gap-2 px-2 py-2 text-left">
          <Icon aria-hidden className="text-muted-foreground mt-0.5 size-4 shrink-0" />
          <span className="flex min-w-0 flex-col gap-0.5">
            <span className="truncate text-sm font-medium">{entry.directory ? entry.name : entry.name.replace(/\.md$/i, "")}</span>
            {entry.path && <span className="text-muted-foreground truncate text-xs">{entry.path}</span>}
            {entry.snippet && <span className="text-muted-foreground line-clamp-2 text-xs break-words"><Highlight text={entry.snippet} query={query} /></span>}
          </span>
        </button>
        {trash && <Button variant="ghost" size="sm" className="mt-1" disabled={disabled} onClick={() => onRestore(entry)}>{t("恢复")}</Button>}
      </div>;
    })}
  </div>;
}

/** Marks every case-insensitive occurrence as plain text; no HTML from the note is interpreted. */
function Highlight({ text, query }: { text: string; query: string }) {
  const needle = query.trim().toLowerCase();
  if (!needle) return text;
  const parts: { text: string; hit: boolean }[] = [];
  const lower = text.toLowerCase();
  for (let at = 0; at < text.length;) {
    const found = lower.indexOf(needle, at);
    if (found < 0 || lower.length !== text.length) { parts.push({ text: text.slice(at), hit: false }); break; }
    if (found > at) parts.push({ text: text.slice(at, found), hit: false });
    parts.push({ text: text.slice(found, found + needle.length), hit: true });
    at = found + needle.length;
  }
  return parts.map((part, index) => part.hit ? <mark key={index} className="bg-accent text-foreground rounded-sm">{part.text}</mark> : <span key={index}>{part.text}</span>);
}
