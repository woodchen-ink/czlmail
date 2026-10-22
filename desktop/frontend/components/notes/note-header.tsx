"use client";

import { useEffect, useRef } from "react";
import { AlertTriangle, Check, CloudUpload, GitCompareArrows, MoreHorizontal, Paperclip, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useI18n } from "@/components/locale-provider";
import type { NoteEntry } from "@/lib/notes/api";
import { t } from "@/lib/i18n";

export type NoteMode = "live" | "source" | "read";

/** Note title row: folder path, editable file name, sync state and the per-note actions. */
export function NoteHeader({ note, dirty, mode, busy, focusTitle, onMode, onRename, onAttach, onDiff, onTrash }: {
  note: NoteEntry; dirty: boolean; mode: NoteMode; busy: boolean;
  /** Focus and select the title once, right after the note was created. */
  focusTitle: boolean;
  onMode: (mode: NoteMode) => void; onRename: (name: string) => void;
  onAttach: () => void; onDiff: () => void; onTrash: () => void;
}) {
  useI18n();
  const input = useRef<HTMLInputElement>(null);
  const stem = note.name.replace(/\.md$/i, "");
  const editable = !note.deleted && note.status !== "conflict";
  useEffect(() => {
    if (focusTitle) { input.current?.focus(); input.current?.select(); }
  }, [focusTitle, note.id]);

  function commit() {
    const value = input.current?.value.trim() ?? "";
    if (!value || value === stem) { if (input.current) input.current.value = stem; return; }
    onRename(value);
  }

  return <header className="border-border flex min-h-12 items-center gap-2 border-b px-4">
    <div className="flex min-w-0 flex-1 items-center gap-1 text-sm">
      {note.path && <span className="text-muted-foreground max-w-[40%] shrink-0 truncate" title={note.path}>{note.path.split("/").join(" / ")} /</span>}
      <input key={`${note.id}:${note.name}`} ref={input} defaultValue={stem} disabled={!editable || busy} aria-label={t("笔记名称")} spellCheck={false}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") { event.preventDefault(); event.currentTarget.blur(); }
          if (event.key === "Escape") { event.currentTarget.value = stem; event.currentTarget.blur(); }
        }}
        className="hover:bg-secondary focus-visible:bg-secondary min-w-0 flex-1 truncate rounded-md bg-transparent px-1.5 py-1 font-medium outline-none disabled:hover:bg-transparent" />
    </div>
    <NoteSyncState note={note} dirty={dirty} onDiff={onDiff} />
    <Tooltip><TooltipTrigger asChild><span className="inline-flex"><Button variant="ghost" size="icon-sm" aria-label={t("添加附件")} disabled={busy || mode === "read" || note.deleted} onClick={onAttach}><Paperclip /></Button></span></TooltipTrigger><TooltipContent>{t("添加附件")}</TooltipContent></Tooltip>
    <ToggleGroup type="single" size="sm" value={mode} onValueChange={(value) => { if (value) onMode(value as NoteMode); }}>
      <ToggleGroupItem value="live" aria-label={t("实时预览")}>{t("实时预览")}</ToggleGroupItem>
      <ToggleGroupItem value="source" aria-label={t("源码")}>{t("源码")}</ToggleGroupItem>
      <ToggleGroupItem value="read" aria-label={t("阅读")}>{t("阅读")}</ToggleGroupItem>
    </ToggleGroup>
    <DropdownMenu>
      <DropdownMenuTrigger asChild><Button variant="ghost" size="icon-sm" aria-label={t("更多操作")} disabled={busy}><MoreHorizontal /></Button></DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onDiff}><GitCompareArrows />{t("本地与远端版本对比")}</DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled={note.deleted} onSelect={onTrash}><Trash2 />{t("移入回收站")}</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  </header>;
}

/** One short word for the sync state; a conflict becomes the button that opens the comparison. */
function NoteSyncState({ note, dirty, onDiff }: { note: NoteEntry; dirty: boolean; onDiff: () => void }) {
  if (note.status === "conflict") return <Button variant="ghost" size="sm" className="text-destructive" onClick={onDiff}><AlertTriangle />{t("存在版本冲突")}</Button>;
  if (note.status === "remote_deleted") return <span className="text-muted-foreground text-xs">{t("远端文件已删除，本地副本仍保留")}</span>;
  const synced = note.status === "synced" && !dirty;
  return <Tooltip><TooltipTrigger asChild><span tabIndex={0} className="text-muted-foreground inline-flex shrink-0 items-center gap-1 text-xs">
    {synced ? <Check className="size-3.5" /> : <CloudUpload className="size-3.5" />}{synced ? t("已同步") : t("待同步")}
  </span></TooltipTrigger><TooltipContent>{synced ? t("已与服务器一致") : t("本地已保存，等待同步")}</TooltipContent></Tooltip>;
}
