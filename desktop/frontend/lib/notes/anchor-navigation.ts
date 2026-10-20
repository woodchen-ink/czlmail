import { EditorView } from "@codemirror/view";

/** Mount offscreen destinations before resolving footnote fragments in the virtualized editor. */
export function bindNoteAnchors(dom: HTMLElement, view: EditorView) {
  let frame = 0;
  /** The shared renderer supplies source offsets; the event never requests external navigation. */
  const handle = (event: Event) => {
    const detail = (event as CustomEvent<{ id: string; position?: number }>).detail;
    if (!detail || typeof detail.id !== "string") return;
    event.preventDefault(); event.stopPropagation();
    cancelAnimationFrame(frame);
    if (typeof detail.position === "number" && Number.isFinite(detail.position)) {
      const position = Math.max(0, Math.min(view.state.doc.length, detail.position));
      view.dispatch({ effects: EditorView.scrollIntoView(position, { y: "center" }) });
    }
    let attempts = 0, stable = 0, height = -1;
    /** Late React roots change document height; keep the target anchored until layout settles. */
    const locate = () => {
      if (!view.dom.isConnected) return;
      const target = view.dom.querySelector<HTMLElement>(`[id="${CSS.escape(detail.id)}"]`);
      const scroller = view.scrollDOM;
      const nextHeight = scroller.scrollHeight;
      stable = nextHeight === height ? stable + 1 : 0; height = nextHeight;
      if (target) {
        const bounds = scroller.getBoundingClientRect(), rect = target.getBoundingClientRect();
        if (rect.top < bounds.top || rect.bottom > bounds.bottom) scroller.scrollTop += rect.top - bounds.top - Math.max(0, (bounds.height - rect.height) / 2);
        if (stable >= 12) return;
      }
      if (++attempts < 180) frame = requestAnimationFrame(locate);
    };
    frame = requestAnimationFrame(locate);
  };
  dom.addEventListener("czl-note-anchor", handle);
  return () => { cancelAnimationFrame(frame); dom.removeEventListener("czl-note-anchor", handle); };
}
