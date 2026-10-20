"use client";

import { useEffect, useState, type DragEvent } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { errorMessage } from "@/lib/api";
import { ContextMenu, ContextMenuTrigger, ContextMenuContent, ContextMenuGroup, ContextMenuItem, ContextMenuSeparator } from "@/components/ui/context-menu";
import { noteApi, type NoteEntry } from "@/lib/notes/api";
import { canDropNote } from "@/lib/notes/tree-drag";

type TreeAction = "note" | "folder" | "rename" | "trash";
interface TreeProps {
  vaultId: string; tick: number; selectedId: string; disabled?: boolean;
  onOpen: (entry: NoteEntry) => void; onFolder: (entry: NoteEntry) => void;
  onAction: (entry: NoteEntry, action: TreeAction) => void;
  onRootAction: (action: "note" | "folder") => void;
  onMove: (entry: NoteEntry, parentId: string) => void;
}
interface DragState {
  entry: NoteEntry | null; target: string | null;
  start: (event: DragEvent, entry: NoteEntry) => void;
  end: () => void;
  over: (event: DragEvent, parentId: string, ancestors: string[]) => void;
  leave: (event: DragEvent) => void;
  drop: (event: DragEvent, parentId: string, ancestors: string[]) => void;
}

/** One key per directory preserves nested expansion across collapsing parents and sync refreshes. */
function expansionKey(vaultId: string, parentId: string) {
  return `czlmail.notes.expanded.${vaultId}.${parentId || "root"}`;
}

/** The blank tree area always represents the root, independently of the current selection. */
export function NoteTree(props: TreeProps) {
  useI18n();
  const [entry, setEntry] = useState<NoteEntry | null>(null);
  const [target, setTarget] = useState<string | null>(null);
  /** Trust only the entry captured by this tree, never a payload supplied by another window. */
  const drag: DragState = {
    entry, target,
    start: (event, item) => {
      event.stopPropagation();
      if (props.disabled) { event.preventDefault(); return; }
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData("application/x-czl-note", item.id);
      setEntry(item);
    },
    end: () => { setEntry(null); setTarget(null); },
    over: (event, parentId, ancestors) => {
      event.stopPropagation();
      if (!canDropNote(entry, props.vaultId, parentId, ancestors, !!props.disabled)) { event.dataTransfer.dropEffect = "none"; setTarget(null); return; }
      event.preventDefault(); event.dataTransfer.dropEffect = "move"; setTarget(parentId);
    },
    leave: (event) => {
      if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setTarget(null);
    },
    drop: (event, parentId, ancestors) => {
      event.preventDefault(); event.stopPropagation();
      if (canDropNote(entry, props.vaultId, parentId, ancestors, !!props.disabled) && entry) props.onMove(entry, parentId);
      setEntry(null); setTarget(null);
    },
  };
  return <div className="flex min-h-full flex-col">
    <NoteTreeBranch {...props} parentId="" ancestors={[]} drag={drag} />
    <ContextMenu><ContextMenuTrigger asChild disabled={props.disabled}>
      <div aria-label={t("笔记根目录")} onDragOver={(event) => drag.over(event, "", [])} onDragLeave={drag.leave} onDrop={(event) => drag.drop(event, "", [])}
        className={cn("min-h-16 flex-1 rounded-md", target === "" && "bg-accent ring-ring ring-2 ring-inset")}>
        {entry && <p className="text-muted-foreground px-2 py-4 text-center text-sm">{t("拖到此处移至根目录")}</p>}
      </div>
    </ContextMenuTrigger><ContextMenuContent><ContextMenuGroup>
      <ContextMenuItem onSelect={() => props.onRootAction("note")}>{t("新建笔记")}</ContextMenuItem>
      <ContextMenuItem onSelect={() => props.onRootAction("folder")}>{t("新建文件夹")}</ContextMenuItem>
    </ContextMenuGroup></ContextMenuContent></ContextMenu>
  </div>;
}

/** Obsidian-style lazy branches share one drag session and preserve expansion per parent. */
function NoteTreeBranch({ vaultId, parentId, ancestors, tick, selectedId, disabled, onOpen, onFolder, onAction, drag, ...rest }: TreeProps & { parentId: string; ancestors: string[]; drag: DragState }) {
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

  /** Persist expansion immediately; a successful drop destination is opened for visibility. */
  function expand(entry: NoteEntry, open: boolean) {
    const next = new Set(expanded);
    if (open) next.add(entry.id); else next.delete(entry.id);
    setExpanded(next);
    try { localStorage.setItem(expansionKey(vaultId, parentId), JSON.stringify([...next])); } catch { /* Ignore unavailable preference storage. */ }
  }

  return <div className="flex flex-col gap-0.5" role="group">
    {entries.map((entry) => <div key={entry.id}>
      <ContextMenu><ContextMenuTrigger asChild disabled={disabled}><button type="button" title={entry.name} aria-expanded={entry.directory ? expanded.has(entry.id) : undefined}
        aria-current={selectedId === entry.id ? "true" : undefined} disabled={disabled} draggable={!disabled}
        onDragStart={(event) => drag.start(event, entry)} onDragEnd={drag.end}
        onDragOver={(event) => { if (entry.directory) drag.over(event, entry.id, [...ancestors, entry.id]); else { event.stopPropagation(); event.dataTransfer.dropEffect = "none"; } }}
        onDragLeave={drag.leave}
        onDrop={(event) => { if (entry.directory) { if (canDropNote(drag.entry, vaultId, entry.id, [...ancestors, entry.id], !!disabled)) expand(entry, true); drag.drop(event, entry.id, [...ancestors, entry.id]); } else { event.preventDefault(); event.stopPropagation(); drag.end(); } }}
        onClick={() => { if (entry.directory) { expand(entry, !expanded.has(entry.id)); onFolder(entry); } else onOpen(entry); }}
        className={cn("text-muted-foreground hover:bg-secondary hover:text-foreground focus-visible:ring-ring flex min-h-9 w-full items-center gap-2 rounded-md px-1.5 py-1.5 text-left text-sm focus-visible:ring-2 focus-visible:outline-none", selectedId === entry.id && "bg-muted text-foreground font-medium", drag.target === entry.id && "bg-accent ring-ring ring-2 ring-inset")}>
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
        <NoteTreeBranch {...rest} vaultId={vaultId} parentId={entry.id} ancestors={[...ancestors, entry.id]} tick={tick} selectedId={selectedId} disabled={disabled} onOpen={onOpen} onFolder={onFolder} onAction={onAction} drag={drag} />
      </div>}
    </div>)}
  </div>;
}
