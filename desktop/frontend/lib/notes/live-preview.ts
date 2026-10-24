import { createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { EditorState, StateEffect, StateField, type Range } from "@codemirror/state";
import { Decoration, EditorView, WidgetType, type DecorationSet } from "@codemirror/view";
import { ensureSyntaxTree, syntaxTree } from "@codemirror/language";
import { frontmatter } from "@/lib/notes/markdown";
import { NoteMarkdown } from "@/components/notes/note-markdown";
import { noteMarkdownClassName } from "@/lib/notes/markdown-style";
import { bindNoteAnchors } from "@/lib/notes/anchor-navigation";
import { caretOffset, toggleTask } from "@/lib/notes/live-editing";
import { t } from "@/lib/i18n";

const widgetRoots = new WeakMap<HTMLElement, Root>();
const widgetKeys = new WeakMap<HTMLElement, string>();
/** Measured block heights keep the scrollbar steady while offscreen blocks are virtualized. */
const heights = new Map<string, number>();
/** Footnotes and reference definitions make a block's output depend on the rest of the note. */
const crossBlockSyntax = /\[\^[^\]\n]+\]|^ {0,3}\[[^\]\n]+\]:/m;

/**
 * A rendered block. `key` is the block's own source unless the note has cross-block syntax, so
 * typing in one block leaves every other widget (React root, images, height) untouched.
 * Positions are read from the DOM at event time: a reused widget's own offsets may be stale.
 */
class MarkdownWidget extends WidgetType {
  constructor(readonly text: string, readonly from: number, readonly to: number, readonly key: string, readonly vaultId: string, readonly noteId: string) { super(); }
  eq(other: MarkdownWidget) { return this.key === other.key && this.vaultId === other.vaultId && this.noteId === other.noteId; }
  get estimatedHeight() { return heights.get(this.key) ?? -1; }
  toDOM(view: EditorView) {
    const container = document.createElement("div"); container.className = "group relative flow-root";
    const content = container.appendChild(document.createElement("div")); content.className = noteMarkdownClassName;
    widgetRoots.set(container, createRoot(content));
    this.render(container, view);
    const button = container.appendChild(document.createElement("button"));
    button.type = "button"; button.textContent = t("编辑");
    button.className = "bg-background text-muted-foreground absolute top-0 right-0 rounded-md px-2 text-sm opacity-0 group-hover:opacity-100 focus-visible:opacity-100";
    button.hidden = view.state.readOnly || this.from === this.to;
    button.onclick = () => edit(view, view.posAtDOM(container));
    content.onclick = (event) => {
      if (view.state.readOnly || (event.target as HTMLElement).closest("a,button,img,input,summary,details,pre")) return;
      edit(view, caretAt(container, view, event));
    };
    const observer = new ResizeObserver(() => {
      const key = widgetKeys.get(container);
      if (key !== undefined) { if (heights.size > 2000) heights.clear(); heights.set(key, container.getBoundingClientRect().height); }
      view.requestMeasure();
    });
    observer.observe(content);
    container.addEventListener("load", () => view.requestMeasure(), true);
    const unbind = bindNoteAnchors(container, view);
    container.addEventListener("czl-note-destroy", () => { observer.disconnect(); unbind(); }, { once: true });
    return container;
  }
  /** Reuse React and image DOM when the block changed; React reconciles instead of remounting. */
  updateDOM(dom: HTMLElement, view: EditorView) {
    if (!widgetRoots.has(dom)) return false;
    this.render(dom, view);
    return true;
  }
  private render(dom: HTMLElement, view: EditorView) {
    dom.dataset.from = String(this.from);
    widgetKeys.set(dom, this.key);
    const onTask = view.state.readOnly ? undefined : (from: number) => {
      const change = toggleTask(view.state.doc.toString(), from + view.posAtDOM(dom) - Number(dom.dataset.from));
      if (change) view.dispatch({ changes: change });
    };
    widgetRoots.get(dom)?.render(createElement(NoteMarkdown, { vaultId: this.vaultId, noteId: this.noteId, text: this.text, range: { from: this.from, to: this.to, footnotesOnly: this.from === this.to }, onTask, onLink: () => {} }));
  }
  destroy(dom: HTMLElement) {
    dom.dispatchEvent(new Event("czl-note-destroy"));
    const root = widgetRoots.get(dom); widgetRoots.delete(dom);
    // Unmounting during a React-driven editor update must not synchronously nest a React render.
    if (root) queueMicrotask(() => root.unmount());
  }
}

/** Bullet and horizontal-rule stand-ins for list markers and `---` lines in the block being edited. */
class SymbolWidget extends WidgetType {
  constructor(readonly kind: "bullet" | "rule") { super(); }
  eq(other: SymbolWidget) { return this.kind === other.kind; }
  toDOM() {
    const el = document.createElement(this.kind === "rule" ? "div" : "span");
    if (this.kind === "bullet") { el.textContent = "•"; el.className = "text-muted-foreground"; }
    else el.className = "border-border my-3 border-t";
    return el;
  }
}

/** A task box that flips its `[ ]` / `[x]` source on click without moving the caret. */
class TaskBoxWidget extends WidgetType {
  constructor(readonly checked: boolean) { super(); }
  eq(other: TaskBoxWidget) { return this.checked === other.checked; }
  toDOM(view: EditorView) {
    const box = document.createElement("input");
    box.type = "checkbox"; box.checked = this.checked; box.disabled = view.state.readOnly; box.className = "mr-1 align-middle";
    box.onmousedown = (event) => event.preventDefault();
    box.onclick = (event) => {
      event.preventDefault();
      const line = view.state.doc.lineAt(view.posAtDOM(box));
      const change = toggleTask(view.state.doc.toString(), line.from);
      if (change) view.dispatch({ changes: change });
    };
    return box;
  }
  ignoreEvent() { return true; }
}

function edit(view: EditorView, anchor: number) {
  view.dispatch({ selection: { anchor } }); view.focus();
}

/** Put the caret where the user clicked, mapping the rendered text before the point back to source. */
function caretAt(dom: HTMLElement, view: EditorView, event: MouseEvent): number {
  const start = view.posAtDOM(dom);
  const delta = start - Number(dom.dataset.from);
  // The text under the pointer decides; event.target can be a wrapper when the click lands between lines.
  const point = document.caretRangeFromPoint?.(event.clientX, event.clientY);
  const node = point?.startContainer;
  const element = (node instanceof Element ? node : node?.parentElement)?.closest<HTMLElement>("[data-note-from]");
  if (!point || !element || !dom.contains(element)) return start;
  const before = document.createRange();
  before.selectNodeContents(element); before.setEnd(point.startContainer, point.startOffset);
  const from = Number(element.dataset.noteFrom) + delta, to = Number(element.dataset.noteTo) + delta;
  if (!Number.isFinite(from) || !Number.isFinite(to) || from < 0 || to > view.state.doc.length) return start;
  return Math.min(to, from + caretOffset(view.state.sliceDoc(from, to), before.toString()));
}

/** Two independent 32-bit FNV-1a hashes plus length: a collision would only reuse a stale widget. */
function digest(text: string) {
  let a = 0x811c9dc5, b = 0x01000193;
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    a = Math.imul(a ^ code, 0x01000193);
    b = Math.imul(b ^ code, 0x5bd1e995) ^ (b >>> 15);
  }
  return `${text.length}.${(a >>> 0).toString(36)}.${(b >>> 0).toString(36)}`;
}

const hidden: Record<string, string[]> = {
  InlineCode: ["CodeMark"], Emphasis: ["EmphasisMark"], StrongEmphasis: ["EmphasisMark"], Strikethrough: ["StrikethroughMark"],
  Link: ["LinkMark", "URL", "LinkTitle"],
  ...Object.fromEntries([1, 2, 3, 4, 5, 6].map((n) => [`ATXHeading${n}`, ["HeaderMark"]])),
};

export const setFocus = StateEffect.define<boolean>();
/** Tracks editor focus in state so the decorations rebuild when it is lost or gained. */
const focused = StateField.define<boolean>({
  create: () => false,
  update: (value, transaction) => transaction.effects.reduce((acc, effect) => effect.is(setFocus) ? effect.value : acc, value),
  provide: () => EditorView.focusChangeEffect.of((_state, focusing) => setFocus.of(focusing)),
});

/** Top-level blocks render like reading mode; only the block under the caret exposes source. */
export function livePreview(vaultId: string, noteId: string) {
  /** Replacing whole blocks preserves tables, list nesting, quotes and fenced code as one unit. */
  const build = (state: EditorState): DecorationSet => {
    const ranges: Range<Decoration>[] = [];
    const text = state.doc.toString(); const metadata = frontmatter(text);
    // Cross-block notes key on a digest, never the text itself: the height cache keeps up to 2000 keys.
    const shared = crossBlockSyntax.test(text) ? digest(text) : "";
    const tree = ensureSyntaxTree(state, state.doc.length, 100) ?? syntaxTree(state);
    /** Keep every selection's active block in source, without ever mutating the document. */
    const block = (from: number, to: number): boolean => {
      // An editor without focus renders every block: the caret no longer says where the user is writing.
      const active = state.field(focused) && state.selection.ranges.some((range) => range.empty ? range.from >= from && (range.from < to || to === state.doc.length && range.from === to) : range.from < to && range.to > from);
      if (active || to <= from) return false;
      const key = shared ? `${from}:${to}:${shared}` : text.slice(from, to);
      ranges.push(Decoration.replace({ widget: new MarkdownWidget(text, from, to, key, vaultId, noteId), block: true }).range(from, to));
      return true;
    };
    /** A fence being edited stays source but looks like a code box, including while it is still unclosed. */
    const codeBox = (from: number, to: number) => {
      const first = state.doc.lineAt(from).number, last = state.doc.lineAt(Math.max(from, to - 1)).number;
      for (let n = first; n <= last; n++) {
        const edge = (n === first ? " cm-note-code-first" : "") + (n === last ? " cm-note-code-last" : "");
        ranges.push(Decoration.line({ class: `cm-note-code${edge}` }).range(state.doc.line(n).from));
      }
    };
    /** Inline code, emphasis and strikethrough in the block being edited: markers hide until the caret touches the span. */
    const inline = (from: number, to: number) => {
      const touches = (a: number, b: number) => state.selection.ranges.some((range) => range.from <= b && range.to >= a);
      /** Hide a marker and the single space that follows it ("# ", "> "). */
      const hide = (a: number, b: number, space = false) => {
        if (space && state.sliceDoc(b, b + 1) === " ") b++;
        if (b > a) ranges.push(Decoration.replace({}).range(a, b));
      };
      tree.iterate({ from, to, enter(node) {
        if (node.name === "InlineCode") ranges.push(Decoration.mark({ class: "cm-note-inline-code" }).range(node.from, node.to));
        if (node.name === "Blockquote") {
          for (let n = state.doc.lineAt(node.from).number, last = state.doc.lineAt(node.to).number; n <= last; n++) ranges.push(Decoration.line({ class: "cm-note-quote" }).range(state.doc.line(n).from));
          return;
        }
        if (node.name === "QuoteMark") {
          const line = state.doc.lineAt(node.from);
          if (!touches(line.from, line.to)) hide(node.from, node.to, true);
          return;
        }
        const line = () => state.doc.lineAt(node.from);
        if (node.name === "ListMark" && node.node.parent?.parent?.name === "BulletList" && !touches(line().from, line().to)) {
          ranges.push(Decoration.replace({ widget: new SymbolWidget("bullet") }).range(node.from, node.to));
          return;
        }
        if (node.name === "TaskMarker" && !touches(line().from, line().to)) {
          const checked = /x/i.test(state.sliceDoc(node.from, node.to));
          ranges.push(Decoration.replace({ widget: new TaskBoxWidget(checked) }).range(node.from, node.to));
          if (checked) ranges.push(Decoration.line({ class: "cm-note-done" }).range(line().from));
          return;
        }
        if (node.name === "HorizontalRule" && !touches(node.from, node.to)) {
          ranges.push(Decoration.replace({ widget: new SymbolWidget("rule") }).range(node.from, node.to));
          return;
        }
        const names = hidden[node.name];
        if (!names || touches(node.from, node.to)) return;
        const cursor = node.node.cursor();
        if (!cursor.firstChild()) return;
        do { if (names.includes(cursor.name)) hide(cursor.from, cursor.to, cursor.name === "HeaderMark"); } while (cursor.nextSibling());
      } });
    };
    if (metadata) block(0, metadata.to);
    for (let node = tree.topNode.firstChild; node; node = node.nextSibling) {
      if (metadata && node.from < metadata.to) continue;
      if (block(node.from, node.nextSibling?.from ?? state.doc.length)) continue;
      if (node.name === "FencedCode") codeBox(node.from, node.to);
      else inline(node.from, node.to);
    }
    if (/\[\^[^\]]+\]/.test(text)) ranges.push(Decoration.widget({ widget: new MarkdownWidget(text, text.length, text.length, `footnotes:${shared || digest(text)}`, vaultId, noteId), block: true, side: 1 }).range(text.length));
    return Decoration.set(ranges, true);
  };
  return StateField.define<DecorationSet>({
    create: build,
    update: (value, transaction) => transaction.docChanged || transaction.selection || transaction.effects.some((effect) => effect.is(setFocus)) || syntaxTree(transaction.startState) !== syntaxTree(transaction.state) ? build(transaction.state) : value,
    // Rendered prose must never be atomic: Backspace/Delete must edit characters, not whole blocks.
    provide: (field) => [focused, EditorView.decorations.from(field)],
  });
}
