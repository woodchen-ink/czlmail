import { EditorState, StateEffect } from "@codemirror/state";
import { history } from "@codemirror/commands";
import { EditorView } from "@codemirror/view";

export interface NoteViewport { from: number; offset: number; scrollTop: number; mode: "source" | "live" | "read" }
interface NoteSession { state?: EditorState; viewport?: NoteViewport }
const sessions = new Map<string, NoteSession>();
const limit = 12;

/** Session-only LRU: never persist note contents/history in localStorage or keep mounted editors. */
export function noteSession(vaultId: string, noteId: string): NoteSession {
  const key = JSON.stringify([vaultId, noteId]);
  const value = sessions.get(key) ?? {};
  sessions.delete(key); sessions.set(key, value);
  while (sessions.size > limit) sessions.delete(sessions.keys().next().value!);
  return value;
}

/** Retain undo and selection, but release document widgets, React callbacks and DOM closures. */
export function retainEditorState(state: EditorState): EditorState {
  return state.update({ effects: StateEffect.reconfigure.of(history()) }).state;
}

/** Store the top visible source block and its offset rather than a percentage of document height. */
export function editorViewport(view: EditorView, mode: "source" | "live"): NoteViewport {
  const top = view.scrollDOM.getBoundingClientRect().top;
  const block = view.lineBlockAtHeight(Math.max(0, top - view.documentTop));
  return { from: block.from, offset: block.top + view.documentTop - top, scrollTop: view.scrollDOM.scrollTop, mode };
}

/** Materialize a virtual block before restoring its pixel offset; cleanup cancels deferred work. */
export function restoreEditorViewport(view: EditorView, viewport: NoteViewport | undefined, mode: "source" | "live"): () => void {
  if (!viewport) return () => {};
  const from = Math.min(view.state.doc.length, viewport.from);
  view.dispatch({ effects: EditorView.scrollIntoView(from, { y: "start", yMargin: 0 }) });
  let canceled = false;
  const frame = requestAnimationFrame(() => view.requestMeasure({
    read: () => viewport.mode === mode ? viewport.scrollTop : view.lineBlockAt(from).top + view.documentTop - view.scrollDOM.getBoundingClientRect().top + view.scrollDOM.scrollTop - viewport.offset,
    write: (top) => { if (!canceled) view.scrollDOM.scrollTop = top; },
  }));
  return () => { canceled = true; cancelAnimationFrame(frame); };
}

const blockSelector = "[data-note-from]:is(p,li,h1,h2,h3,h4,h5,h6,blockquote,table,pre,code)";

/** Locate the first visible rendered block, preferring its deepest source-mapped child. */
export function readingViewport(host: HTMLElement): NoteViewport {
  const top = host.getBoundingClientRect().top;
  const blocks = Array.from(host.querySelectorAll<HTMLElement>(blockSelector));
  const visible = blocks.filter((element) => element.getBoundingClientRect().bottom > top);
  const element = visible.findLast((item) => item.getBoundingClientRect().top <= top + 1) ?? visible[0];
  return { from: Number(element?.dataset.noteFrom ?? 0), offset: element ? element.getBoundingClientRect().top - top : 0, scrollTop: host.scrollTop, mode: "read" };
}

/** Match the editor's source anchor to rendered blocks, including repeated headings and lists. */
export function restoreReadingViewport(host: HTMLElement, viewport: NoteViewport | undefined) {
  if (!viewport) { host.scrollTop = 0; return; }
  if (viewport.mode === "read") { host.scrollTop = viewport.scrollTop; return; }
  const blocks = Array.from(host.querySelectorAll<HTMLElement>(blockSelector));
  const element = blocks.findLast((item) => Number(item.dataset.noteFrom) <= viewport.from && Number(item.dataset.noteTo) > viewport.from)
    ?? blocks.findLast((item) => Number(item.dataset.noteFrom) <= viewport.from) ?? blocks[0];
  if (element) host.scrollTop += element.getBoundingClientRect().top - host.getBoundingClientRect().top - viewport.offset;
}
