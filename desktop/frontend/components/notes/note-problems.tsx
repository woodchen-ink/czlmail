"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, RotateCw } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { errorMessage } from "@/lib/api";
import { noteApi, type NoteProblem } from "@/lib/notes/api";
import { failureText } from "@/lib/notes/sync-state";
import { t } from "@/lib/i18n";

/**
 * Everything in the library that did not synchronize, behind a count in the sidebar footer.
 * Each row names the entry and the reason, so a failure is never pinned on whatever note
 * happens to be open. Renders nothing while there is nothing to report.
 */
export function NoteProblems({ vaultId, count, tick, syncing, onOpen, onSync }: {
  vaultId: string; count: number;
  /** Changes whenever the library changes; an open list reloads. */
  tick: number;
  syncing: boolean;
  onOpen: (problem: NoteProblem) => void;
  /** Retry everything: the manual sync. */
  onSync: () => void;
}) {
  const [open, setOpen] = useState(false);
  const [items, setItems] = useState<NoteProblem[]>([]);
  useEffect(() => {
    if (!open || !vaultId) return;
    let live = true;
    noteApi.problems(vaultId).then((list) => { if (live) setItems(list ?? []); }).catch((err) => { if (live) toast.error(errorMessage(err)); });
    return () => { live = false; };
  }, [open, vaultId, tick, count]);
  if (!count) return null;
  const label = t("{0} 项未同步", count);
  return <Popover open={open} onOpenChange={setOpen}>
    <PopoverTrigger asChild>
      <Button variant="ghost" size="sm" className="text-destructive ml-auto" aria-label={label}><AlertTriangle />{label}</Button>
    </PopoverTrigger>
    <PopoverContent align="end" side="top" className="w-88 max-w-[calc(100vw-1rem)] gap-1 p-1.5">
      <div className="flex items-center gap-2 px-1.5 py-1">
        <span className="font-medium">{t("未能同步的项目")}</span><span className="flex-1" />
        <Button variant="outline" size="sm" disabled={syncing} onClick={onSync}><RotateCw className={syncing ? "animate-spin" : undefined} />{t("全部重试")}</Button>
      </div>
      <ul className="czl-scroll max-h-80 overflow-y-auto">
        {items.map((item) => {
          const openable = !!item.id && !item.directory;
          const name = item.name.replace(/\.md$/i, "");
          return <li key={item.id || `remote:${item.name}`} className="flex items-start gap-1">
            <button type="button" disabled={!openable} onClick={() => { setOpen(false); onOpen(item); }}
              className="enabled:hover:bg-secondary focus-visible:ring-ring flex min-w-0 flex-1 flex-col gap-0.5 rounded-md px-1.5 py-1.5 text-left focus-visible:ring-2 focus-visible:outline-none">
              <span className="truncate font-medium">{name}{item.path && <span className="text-muted-foreground font-normal"> · {item.path}</span>}</span>
              <span className="text-muted-foreground text-xs">{item.remoteOnly ? t("服务器上的这个文件没能下载到本机。") : failureText(item.kind)}</span>
              {item.detail && <span className="text-muted-foreground/70 text-xs break-words select-text">{item.detail}</span>}
            </button>
            {item.id && item.kind !== "conflict" && <Button variant="ghost" size="icon-sm" aria-label={t("重试")} className="mt-1"
              onClick={() => void noteApi.retry(vaultId, item.id).catch((err) => toast.error(errorMessage(err)))}><RotateCw /></Button>}
          </li>;
        })}
      </ul>
    </PopoverContent>
  </Popover>;
}
