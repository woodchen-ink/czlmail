"use client";

import { useEffect, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { errorMessage } from "@/lib/api";
import { ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuGroup, ContextMenuItem, ContextMenuSeparator } from "@/components/ui/context-menu";
import { noteApi, type NoteEntry } from "@/lib/notes/api";

/** One key per directory preserves nested expansion across collapsing parents and sync refreshes. */
function expansionKey(vaultId: string, parentId: string) {
  return `czlmail.notes.expanded.${vaultId}.${parentId || "root"}`;
}

/** Obsidian-style tree: aligned disclosure arrows, text-only files and continuous nesting guides. */
export function NoteTree({ vaultId, parentId = "", tick, selectedId, onOpen, onFolder, onAction }: {
  vaultId: string; parentId?: string; tick: number; selectedId: string;
  onOpen: (entry: NoteEntry) => void; onFolder: (entry: NoteEntry) => void;
  onAction: (entry: NoteEntry, action: "note" | "folder" | "rename" | "trash") => void;
}) {
  useI18n();
  const [entries, setEntries] = useState<NoteEntry[]>([]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  useEffect(() => {
    let live = true;
    try {
      const value: unknown = JSON.parse(localStorage.getItem(expansionKey(vaultId, parentId)) || "[]");
      if (Array.isArray(value)) queueMicrotask(() => { if (live) setExpanded(new Set(value.filter((id): id is string => typeof id === "string"))); });
    } catch { /* Corrupt preferences never affect stored notes. */ }
    noteApi.list(vaultId, parentId, "", false).then((list) => { if (live) setEntries(list ?? []); }).catch((err) => { if (live) toast.error(errorMessage(err)); });
    return () => { live = false; };
  }, [vaultId, parentId, tick]);

  /** Toggling writes the preference immediately; directory reloads do not reset it. */
  function toggle(entry: NoteEntry) {
    const next = new Set(expanded);
    if (next.has(entry.id)) next.delete(entry.id); else next.add(entry.id);
    setExpanded(next);
    try { localStorage.setItem(expansionKey(vaultId, parentId), JSON.stringify([...next])); } catch { /* Ignore unavailable preference storage. */ }
    onFolder(entry);
  }

  return <div className="flex flex-col gap-0.5" role="group">
    {entries.map((entry) => <div key={entry.id}>
      <ContextMenu><ContextMenuTrigger asChild><button type="button" title={entry.name} aria-expanded={entry.directory ? expanded.has(entry.id) : undefined}
        aria-current={selectedId === entry.id ? "true" : undefined}
        onClick={() => { if (entry.directory) toggle(entry); else onOpen(entry); }}
        className={cn("text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:ring-ring flex min-h-9 w-full items-center gap-2 rounded-md px-1.5 py-1.5 text-left text-sm focus-visible:ring-2 focus-visible:outline-none", selectedId === entry.id && "bg-muted text-foreground font-medium")}>
        {entry.directory
          ? expanded.has(entry.id) ? <ChevronDown aria-hidden className="size-4 shrink-0 opacity-60" /> : <ChevronRight aria-hidden className="size-4 shrink-0 opacity-60" />
          : <span className="size-4 shrink-0" aria-hidden />}
        <span className="truncate">{entry.directory ? entry.name : entry.name.replace(/\.md$/i, "")}</span>
        {entry.status === "conflict" && <span title={t("存在版本冲突")} className="text-destructive ml-auto">!</span>}
        {entry.status === "pending" && <span title={t("本地已保存，等待同步")} className="ml-auto">•</span>}
      </button></ContextMenuTrigger>
      <ContextMenuContent><ContextMenuGroup>
        {entry.directory && <><ContextMenuItem onSelect={() => onAction(entry, "folder")}>{t("新建子文件夹")}</ContextMenuItem><ContextMenuItem onSelect={() => onAction(entry, "note")}>{t("在此目录新建笔记")}</ContextMenuItem><ContextMenuSeparator /></>}
        <ContextMenuItem onSelect={() => onAction(entry, "rename")}>{t("重命名")}</ContextMenuItem>
        <ContextMenuItem onSelect={() => onAction(entry, "trash")}>{t("移入回收站")}</ContextMenuItem>
      </ContextMenuGroup></ContextMenuContent></ContextMenu>
      {entry.directory && expanded.has(entry.id) && <div className="border-border ml-3.5 border-l pl-2">
        <NoteTree vaultId={vaultId} parentId={entry.id} tick={tick} selectedId={selectedId} onOpen={onOpen} onFolder={onFolder} onAction={onAction} />
      </div>}
    </div>)}
  </div>;
}
