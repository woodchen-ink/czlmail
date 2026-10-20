/* eslint-disable @next/next/no-img-element -- Preview original local image bytes in the Wails WebView. */
"use client";

import { Button } from "@/components/ui/button";
import { NotePDFPreview } from "@/components/notes/note-pdf-preview";
import { noteResource } from "@/lib/notes/api";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

export interface NoteAttachmentPreview { vaultId: string; noteId: string; path: string; name: string; type: string; src?: string }

/** Attachments occupy the document pane; no modal or native PDF plugin is involved. */
export function NoteAttachmentPane({ item, onExternal }: { item: NoteAttachmentPreview; onExternal: () => void }) {
  useI18n();
  const url = item.src ?? noteResource(item.vaultId, item.noteId, item.path);
  return <div className="flex h-full min-h-0 flex-col gap-3 p-4" aria-label={t("附件预览")}>
    <div className="flex justify-end"><Button variant="outline" size="sm" onClick={onExternal}>{t("用系统程序打开")}</Button></div>
    <div className="min-h-0 flex-1">
      {item.type === "application/pdf" ? <NotePDFPreview key={url} url={url} /> : <div className="flex h-full items-center justify-center overflow-auto"><img src={url} alt={item.name} referrerPolicy="no-referrer" className="max-h-full max-w-full object-contain" /></div>}
    </div>
  </div>;
}
