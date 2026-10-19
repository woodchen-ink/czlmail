"use client";

import { useEffect, useRef } from "react";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { MergeView } from "@codemirror/merge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useI18n } from "@/components/locale-provider";
import { t } from "@/lib/i18n";
import type { NoteDiff } from "@/lib/notes/api";

/** Line-level diff view is read-only; choosing a version still requires both revision tokens. */
export function NoteDiffDialog({ diff, onClose, onResolve, busy }: {
  diff: NoteDiff | null; onClose: () => void; onResolve: (strategy: string) => void; busy: boolean;
}) {
  useI18n();
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!diff) return;
    let merge: MergeView | undefined;
    const timer = setTimeout(() => {
      if (!host.current) return;
      const extensions = [EditorState.readOnly.of(true), EditorView.editable.of(false), EditorView.lineWrapping,
        EditorView.theme({ "&": { height: "100%", color: "var(--foreground)", backgroundColor: "var(--background)" }, ".cm-scroller": { overflow: "auto" } })];
      merge = new MergeView({ parent: host.current, a: { doc: diff.local.content ?? "", extensions }, b: { doc: diff.remoteContent ?? "", extensions }, highlightChanges: true, gutter: true });
    }, 0);
    return () => { clearTimeout(timer); merge?.destroy(); };
  }, [diff]);
  return <Dialog open={!!diff} onOpenChange={(open) => { if (!open && !busy) onClose(); }}>
    <DialogContent className="flex h-5/6 max-w-6xl flex-col sm:max-w-6xl">
      <DialogHeader><DialogTitle>{t("本地与远端版本对比")}</DialogTitle><DialogDescription>{diff?.stale ? t("离线缓存对比，不代表远端最新状态。联网后才能解决冲突。") : t("左侧为本地，右侧为当前远端。选择任一版本前都会保留本地恢复副本。")}</DialogDescription></DialogHeader>
      <div className="text-muted-foreground grid grid-cols-2 gap-4 text-sm"><span>{t("本地版本")} · {diff?.local.revision}</span><span>{diff?.remote ? t("远端版本") : t("远端文件不存在")}</span></div>
      <div ref={host} className="border-border min-h-0 flex-1 overflow-auto rounded-md border [&_.cm-mergeView]:h-full [&_.cm-mergeViewEditors]:h-full" />
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="outline" disabled={busy} onClick={onClose}>{t("关闭")}</Button>
        {diff?.local.status === "conflict" && <>
          <Button variant="outline" disabled={busy || diff.stale} onClick={() => onResolve("both")}>{t("保留双方版本")}</Button>
          <Button variant="outline" disabled={busy || diff.stale} onClick={() => onResolve("remote")}>{t("采用远端版本")}</Button>
          <Button disabled={busy || diff.stale} onClick={() => onResolve("local")}>{t("提交本地版本")}</Button>
        </>}
      </div>
    </DialogContent>
  </Dialog>;
}
