/* eslint-disable @next/next/no-img-element -- Native image preview uses original local bytes in the Wails WebView. */
"use client";

import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { NotePDFPreview } from "@/components/notes/note-pdf-preview";
import { noteResource } from "@/lib/notes/api";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

export interface NoteAttachmentPreview { vaultId: string; noteId: string; path: string; name: string; type: string }

/** Safe image/PDF preview with an explicit external-app fallback, never native PDF embedding. */
export function NoteAttachmentDialog({ item, onClose, onExternal }: { item: NoteAttachmentPreview | null; onClose: () => void; onExternal: () => void }) {
  useI18n();
  const url = item ? noteResource(item.vaultId, item.noteId, item.path) : "";
  return <Dialog open={!!item} onOpenChange={(value) => { if (!value) onClose(); }}>
    <DialogContent className="flex h-5/6 flex-col sm:max-w-5xl">
      <DialogHeader><DialogTitle>{item?.name ?? t("附件预览")}</DialogTitle><DialogDescription className="sr-only">{t("附件预览")}</DialogDescription></DialogHeader>
      <div className="min-h-0 flex-1">
        {item?.type === "application/pdf" ? <NotePDFPreview key={url} url={url} /> : item && <div className="flex h-full items-center justify-center overflow-auto"><img src={url} alt={item.name} className="max-h-full max-w-full object-contain" /></div>}
      </div>
      <div className="flex justify-end gap-2"><Button variant="outline" onClick={onExternal}>{t("用系统程序打开")}</Button><Button onClick={onClose}>{t("关闭")}</Button></div>
    </DialogContent>
  </Dialog>;
}
