"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { EditorView } from "@codemirror/view";
import { ListTree, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";
import { noteHeadings } from "@/lib/notes/headings";

/** Floating document tools share one local scroll scope in source, live and reading modes. */
export function NoteDocumentTools({ text, children }: { text: string; children: ReactNode }) {
  useI18n();
  const root = useRef<HTMLDivElement>(null);
  const headings = useMemo(() => noteHeadings(text), [text]);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  useEffect(() => {
    const host = root.current;
    if (!host) return;
    let frame = 0;
    const update = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const editor = host.querySelector<HTMLElement>(".cm-editor");
        const view = editor ? EditorView.findFromDOM(editor) : null;
        if (view) {
          let position = 0;
          try { position = view.lineBlockAtHeight(view.scrollDOM.scrollTop + 36).from; } catch { return; }
          let index = -1; headings.forEach((heading, i) => { if (heading.from <= position) index = i; }); setActive(index);
        } else {
          const top = host.getBoundingClientRect().top + 64;
          let index = -1;
          host.querySelectorAll("article h1,article h2,article h3,article h4,article h5,article h6").forEach((heading, i) => { if (heading.getBoundingClientRect().top <= top) index = i; });
          setActive(index);
        }
      });
    };
    host.addEventListener("scroll", update, true); update();
    return () => { host.removeEventListener("scroll", update, true); cancelAnimationFrame(frame); };
  }, [headings, children]);

  /** Parser offsets navigate the editor; DOM order navigates rendered headings, including duplicates. */
  function jump(index: number) {
    const host = root.current; if (!host) return;
    const editor = host.querySelector<HTMLElement>(".cm-editor");
    const view = editor ? EditorView.findFromDOM(editor) : null;
    if (view) view.dispatch({ effects: EditorView.scrollIntoView(headings[index].from, { y: "start", yMargin: 24 }) });
    else host.querySelectorAll("article h1,article h2,article h3,article h4,article h5,article h6")[index]?.scrollIntoView({ block: "start", behavior: "smooth" });
    setActive(index);
  }

  return <div ref={root} className="relative h-full min-h-0">
    <div className="h-full min-h-0 xl:[&_.cm-content]:!mr-64 xl:[&_.cm-content]:!max-w-[calc(100%-16rem)] xl:[&_article]:mr-64 xl:[&_article]:max-w-[calc(100%-16rem)]">{children}</div>
    <aside aria-label={t("正文工具栏")} className="pointer-events-none absolute top-3 right-3 bottom-4 flex max-w-52 flex-col items-end gap-2">
      <Button variant="outline" size="icon-sm" className="pointer-events-auto xl:hidden" aria-label={t("文档目录")} aria-expanded={open} onClick={() => setOpen((value) => !value)}>{open ? <X /> : <ListTree />}</Button>
      <nav aria-label={t("文档目录")} className={cn("pointer-events-auto max-h-full w-52 overflow-y-auto xl:block", !open && "hidden")}>
        <details open className="bg-card border-border rounded-lg border p-2 shadow-sm">
        <summary className="text-muted-foreground cursor-pointer px-2 py-1 text-sm font-medium">{t("文档目录")}</summary>
        {!headings.length && <p className="text-muted-foreground px-2 py-3 text-sm">{t("暂无标题")}</p>}
        {headings.map((heading, index) => <button key={heading.from} type="button" title={heading.text} aria-current={active === index ? "location" : undefined} onClick={() => jump(index)} style={{ paddingInlineStart: `${8 + (heading.level - 1) * 12}px` }} className={cn("hover:bg-secondary text-muted-foreground block w-full truncate rounded-sm py-1.5 pr-2 text-left text-sm", active === index && "bg-muted text-foreground font-medium")}>{heading.text}</button>)}
      </details>
      </nav>
    </aside>
  </div>;
}
