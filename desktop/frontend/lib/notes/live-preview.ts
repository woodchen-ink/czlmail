import { createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { EditorState, StateField } from "@codemirror/state";
import { Decoration, EditorView, WidgetType, type DecorationSet } from "@codemirror/view";
import { ensureSyntaxTree, syntaxTree } from "@codemirror/language";
import { frontmatter } from "@/lib/notes/markdown";
import { NoteMarkdown, noteMarkdownClassName } from "@/components/notes/note-markdown";
import { bindNoteAnchors } from "@/lib/notes/anchor-navigation";
import { t } from "@/lib/i18n";

const widgetRoots = new WeakMap<HTMLElement, Root>();

/** A rendered block uses the reader's real React renderer, not a second Markdown implementation. */
class MarkdownWidget extends WidgetType {
  constructor(readonly text: string, readonly from: number, readonly to: number, readonly vaultId: string, readonly noteId: string) { super(); }
  eq(other: MarkdownWidget) { return this.text === other.text && this.from === other.from && this.to === other.to && this.vaultId === other.vaultId && this.noteId === other.noteId; }
  toDOM(view: EditorView) {
    const container = document.createElement("div"); container.className = "group relative flow-root";
    const content = container.appendChild(document.createElement("div")); content.className = noteMarkdownClassName;
    const root = createRoot(content); widgetRoots.set(container, root);
    root.render(createElement(NoteMarkdown, { vaultId: this.vaultId, noteId: this.noteId, text: this.text, range: { from: this.from, to: this.to, footnotesOnly: this.from === this.to }, onLink: () => {} }));
    this.bindEditing(container, view);
    const observer = new ResizeObserver(() => view.requestMeasure()); observer.observe(content);
    container.addEventListener("load", () => view.requestMeasure(), true);
    // Unmounting during a React-driven editor update must not synchronously nest a React render.
    const unbind = bindNoteAnchors(container, view);
    container.addEventListener("czl-note-destroy", () => { observer.disconnect(); unbind(); }, { once: true });
    return container;
  }
  /** Reuse React and image DOM when preceding edits only shift this block's offsets. */
  updateDOM(dom: HTMLElement, view: EditorView) {
    const root = widgetRoots.get(dom);
    if (!root) return false;
    root.render(createElement(NoteMarkdown, { vaultId: this.vaultId, noteId: this.noteId, text: this.text, range: { from: this.from, to: this.to, footnotesOnly: this.from === this.to }, onLink: () => {} }));
    this.bindEditing(dom, view);
    return true;
  }
  /** Rebind positions after document changes instead of retaining a stale edit target. */
  private bindEditing(dom: HTMLElement, view: EditorView) {
    const content = dom.firstElementChild as HTMLElement;
    let button = dom.querySelector<HTMLButtonElement>(":scope > button");
    if (!button) { button = dom.appendChild(document.createElement("button")); button.type = "button"; }
    const edit = () => { if (!view.state.readOnly) { view.dispatch({ selection: { anchor: this.from } }); view.focus(); } };
    button.textContent = t("编辑"); button.hidden = view.state.readOnly || this.from === this.to; button.onclick = edit;
    button.className = "bg-background text-muted-foreground absolute top-0 right-0 rounded-md px-2 text-sm opacity-0 group-hover:opacity-100 focus-visible:opacity-100";
    content.onclick = (event) => { if (!(event.target as HTMLElement).closest("a,button,img,input,summary,details,pre")) edit(); };
  }
  destroy(dom: HTMLElement) {
    dom.dispatchEvent(new Event("czl-note-destroy"));
    const root = widgetRoots.get(dom); widgetRoots.delete(dom);
    if (root) queueMicrotask(() => root.unmount());
  }
}

/** Top-level blocks render identically to reading; only the block under the caret exposes source. */
export function livePreview(vaultId: string, noteId: string) {
  /** Replacing whole blocks preserves tables, list nesting, quotes and fenced code as one unit. */
  const build = (state: EditorState): DecorationSet => {
    const ranges: import("@codemirror/state").Range<Decoration>[] = [];
    const text = state.doc.toString(); const metadata = frontmatter(text);
    const tree = ensureSyntaxTree(state, state.doc.length, 100) ?? syntaxTree(state);
    /** Keep every selection's active block in source, without ever mutating the document. */
    const block = (from: number, to: number) => {
      const active = state.selection.ranges.some((range) => range.empty ? range.from >= from && (range.from < to || to === state.doc.length && range.from === to) : range.from < to && range.to > from);
      if (!active && to > from) ranges.push(Decoration.replace({ widget: new MarkdownWidget(text, from, to, vaultId, noteId), block: true }).range(from, to));
    };
    if (metadata) block(0, metadata.to);
    for (let node = tree.topNode.firstChild; node; node = node.nextSibling) {
      if (metadata && node.from < metadata.to) continue;
      block(node.from, node.nextSibling?.from ?? state.doc.length);
    }
    if (/\[\^[^\]]+\]/.test(text)) ranges.push(Decoration.widget({ widget: new MarkdownWidget(text, text.length, text.length, vaultId, noteId), block: true, side: 1 }).range(text.length));
    return Decoration.set(ranges, true);
  };
  return StateField.define<DecorationSet>({
    create: build,
    update: (value, transaction) => transaction.docChanged || transaction.selection || syntaxTree(transaction.startState) !== syntaxTree(transaction.state) ? build(transaction.state) : value,
    // Rendered prose must never be atomic: Backspace/Delete must edit characters, not whole blocks.
    provide: (field) => EditorView.decorations.from(field),
  });
}
