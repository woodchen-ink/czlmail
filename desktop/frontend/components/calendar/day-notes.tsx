"use client";

import { CalendarDays, FileText, NotebookPen } from "lucide-react";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import { useI18n } from "@/components/locale-provider";
import { requestOpenNote, type NoteDayItem } from "@/lib/notes/api";
import { t } from "@/lib/i18n";

/**
 * Marker next to a calendar date when that day has a journal or notes created/edited on it.
 * Hover lists them; clicking the marker opens the journal (or the first note), clicking an item opens it.
 */
export function DayNotes({ notes }: { notes: NoteDayItem[] | undefined }) {
  useI18n();
  if (!notes?.length) return null;
  const label = notes[0].journal ? t("当天日记") : t("当天笔记 {0} 篇", notes.length);
  return <HoverCard>
    <HoverCardTrigger asChild>
      <button type="button" aria-label={label}
        onClick={(event) => { event.stopPropagation(); requestOpenNote(notes[0]); }}
        className="text-muted-foreground hover:text-foreground focus-visible:ring-ring inline-flex h-5 items-center gap-0.5 rounded px-0.5 text-[11px] tabular-nums focus-visible:ring-2 focus-visible:outline-none">
        <NotebookPen className="size-3.5" />{notes.length > 1 && notes.length}
      </button>
    </HoverCardTrigger>
    <HoverCardContent align="start" onClick={(event) => event.stopPropagation()}>
      <div className="flex flex-col gap-0.5">
        {notes.map((note) => {
          const Icon = note.journal ? CalendarDays : FileText;
          return <button key={note.id} type="button" onClick={() => requestOpenNote(note)}
            className="hover:bg-secondary flex min-w-0 items-start gap-2 rounded-md px-2 py-1.5 text-left">
            <Icon aria-hidden className="text-muted-foreground mt-0.5 size-4 shrink-0" />
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-sm">{note.name.replace(/\.md$/i, "")}</span>
              <span className="text-muted-foreground truncate text-xs">{note.journal ? t("日记") : note.path || t("笔记根目录")}</span>
            </span>
          </button>;
        })}
      </div>
    </HoverCardContent>
  </HoverCard>;
}
