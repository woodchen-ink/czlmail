"use client";

import { useEffect, useRef, useState } from "react";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { Button } from "@/components/ui/button";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

/** Render one PDF page with bundled PDF.js, not WebView's unavailable native PDF plugin. */
export function NotePDFPreview({ url }: { url: string }) {
  useI18n();
  const canvas = useRef<HTMLCanvasElement>(null);
  const [document, setDocument] = useState<PDFDocumentProxy | null>(null);
  const [page, setPage] = useState(1);
  const [error, setError] = useState("");
  useEffect(() => {
    let disposed = false;
    let loading: import("pdfjs-dist").PDFDocumentLoadingTask | undefined;
    void import("pdfjs-dist").then((pdf) => {
      if (disposed) return;
      pdf.GlobalWorkerOptions.workerSrc = "/pdfjs/pdf.worker.min.mjs";
      loading = pdf.getDocument({ url, cMapUrl: "/pdfjs/cmaps/", cMapPacked: true, standardFontDataUrl: "/pdfjs/standard_fonts/", wasmUrl: "/pdfjs/wasm/", disableRange: true });
      return loading.promise;
    }).then((value) => { if (!disposed && value) { setDocument(value); setPage(1); } }).catch(() => { if (!disposed) setError(t("PDF 预览失败，请用系统程序打开。")); });
    return () => { disposed = true; void loading?.destroy(); };
  }, [url]);
  useEffect(() => {
    if (!document || !canvas.current) return;
    let disposed = false;
    let rendering: import("pdfjs-dist").RenderTask | undefined;
    const target = canvas.current;
    void document.getPage(page).then((value) => {
      if (disposed) return;
      const base = value.getViewport({ scale: 1 });
      const width = Math.min(target.parentElement?.clientWidth ?? 800, 1400);
      const ratio = Math.min(window.devicePixelRatio || 1, 2);
      const viewport = value.getViewport({ scale: Math.max(0.25, width / base.width) * ratio });
      target.width = viewport.width; target.height = viewport.height;
      target.style.width = `${viewport.width / ratio}px`; target.style.height = `${viewport.height / ratio}px`;
      rendering = value.render({ canvas: target, viewport }); return rendering.promise;
    }).catch((err: unknown) => { if (!disposed && !(err instanceof Error && err.name === "RenderingCancelledException")) setError(t("PDF 预览失败，请用系统程序打开。")); });
    return () => { disposed = true; rendering?.cancel(); };
  }, [document, page]);
  return <div className="flex h-full min-h-0 flex-col gap-3">
    <div className="flex items-center justify-center gap-3"><Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>{t("上一页")}</Button><span className="text-sm">{page} / {document?.numPages ?? "—"}</span><Button variant="outline" size="sm" disabled={!document || page >= document.numPages} onClick={() => setPage((value) => value + 1)}>{t("下一页")}</Button></div>
    {error ? <p className="text-destructive text-sm">{error}</p> : <div className="min-h-0 flex-1 overflow-auto"><canvas ref={canvas} className="mx-auto" /></div>}
  </div>;
}
