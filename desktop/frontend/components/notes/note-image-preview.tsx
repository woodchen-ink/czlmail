"use client";

import { useState } from "react";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { imageTarget } from "@/lib/notes/markdown";

/** Local and remote images load by default; failed loads remain explicitly retryable. */
export function NoteImagePreview({ src, alt, vaultId, noteId }: { src: string; alt: string; vaultId: string; noteId: string }) {
  useI18n();
  const [failed, setFailed] = useState(false);
  const target = imageTarget(src, vaultId, noteId);
  if (!target) return <span>{t("不支持的图片地址")}</span>;
  if (failed) return <button type="button" className="bg-muted text-muted-foreground my-2 block rounded-md px-4 py-3 text-sm" onClick={() => setFailed(false)}>{t("图片加载失败，点击重试")} · {alt}</button>;
  // eslint-disable-next-line @next/next/no-img-element
  return <img data-note-path={target.remote ? undefined : src} data-note-remote={target.remote ? target.src : undefined} src={target.src} alt={alt} referrerPolicy="no-referrer" loading="lazy" onError={() => setFailed(true)} className="my-3 max-h-96 max-w-full cursor-zoom-in rounded-md object-contain" />;
}
