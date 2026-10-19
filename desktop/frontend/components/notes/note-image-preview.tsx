"use client";

import { useState } from "react";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { imageTarget } from "@/lib/notes/markdown";

/** Remote image loading requires an explicit click and never sends the document as a referrer. */
export function NoteImagePreview({ src, alt, vaultId, noteId }: { src: string; alt: string; vaultId: string; noteId: string }) {
  useI18n();
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const target = imageTarget(src, vaultId, noteId);
  if (!target) return <span>{t("不支持的图片地址")}</span>;
  if (target.remote && !loaded || failed) return <button type="button" className="bg-muted text-muted-foreground my-2 block rounded-md px-4 py-3 text-sm" onClick={() => { setFailed(false); setLoaded(true); }}>{failed ? t("图片加载失败，点击重试") : t("点击加载远程图片")} · {alt}</button>;
  // eslint-disable-next-line @next/next/no-img-element
  return <img data-note-path={target.remote ? undefined : src} src={target.src} alt={alt} referrerPolicy="no-referrer" loading="lazy" onError={() => setFailed(true)} className="my-3 max-h-96 max-w-full rounded-md object-contain" />;
}
