"use client";

import { useEffect, useRef } from "react";
import { Compartment, EditorSelection, EditorState, Transaction } from "@codemirror/state";
import { EditorView, keymap, type Command } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { HighlightStyle, defaultHighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags } from "@lezer/highlight";
import { searchKeymap, highlightSelectionMatches } from "@codemirror/search";
import { livePreview } from "@/lib/notes/live-preview";
import { frontmatter } from "@/lib/notes/markdown";

const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "transparent", color: "var(--foreground)" },
  ".cm-scroller": { overflow: "auto", fontFamily: "inherit", fontSize: "16px", lineHeight: "2" },
  ".cm-content": { padding: "24px 32px 160px", maxWidth: "896px", margin: "0 auto", width: "100%" },
  "&.cm-focused": { outline: "none", border: "none", boxShadow: "none" },
  ".cm-content:focus, .cm-content:focus-visible, .cm-scroller:focus-visible": { outline: "none", boxShadow: "none" },
  ".cm-cursor": { borderLeftColor: "var(--foreground)" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": { backgroundColor: "var(--accent)" },
  ".cm-panels": { backgroundColor: "var(--card)", color: "var(--foreground)" },
});

/** Live mode sizes headings like the rendered block, so entering a block for editing does not jump. */
const liveHeadings = syntaxHighlighting(HighlightStyle.define([
  { tag: tags.heading1, fontSize: "1.875rem", fontWeight: "700", textDecoration: "none" },
  { tag: tags.heading2, fontSize: "1.5rem", fontWeight: "600", textDecoration: "none" },
  { tag: tags.heading3, fontSize: "1.25rem", fontWeight: "600", textDecoration: "none" },
  { tag: [tags.heading4, tags.heading5, tags.heading6], fontWeight: "600", textDecoration: "none" },
  { tag: tags.processingInstruction, color: "var(--muted-foreground)" },
  { tag: tags.monospace, fontFamily: "var(--font-mono, ui-monospace, monospace)", fontSize: "0.875rem" },
]));

/** Wrap every selection in a marker pair, or remove it when the selection is already wrapped. */
function wrap(marker: string): Command {
  return (view) => {
    if (view.state.readOnly) return false;
    view.dispatch(view.state.changeByRange((range) => {
      const doc = view.state.doc, size = marker.length;
      if (doc.sliceString(range.from - size, range.from) === marker && doc.sliceString(range.to, range.to + size) === marker) {
        return { changes: [{ from: range.from - size, to: range.from }, { from: range.to, to: range.to + size }], range: EditorSelection.range(range.from - size, range.to - size) };
      }
      return { changes: [{ from: range.from, insert: marker }, { from: range.to, insert: marker }], range: EditorSelection.range(range.from + size, range.to + size) };
    }));
    return true;
  };
}

/** Turn the selection into link text and leave the caret inside the empty target. */
const link: Command = (view) => {
  if (view.state.readOnly) return false;
  view.dispatch(view.state.changeByRange((range) => {
    const label = view.state.sliceDoc(range.from, range.to);
    return { changes: { from: range.from, to: range.to, insert: `[${label}]()` }, range: EditorSelection.cursor(range.from + label.length + 3) };
  }));
  return true;
};

const formatKeymap = [
  { key: "Mod-b", run: wrap("**") },
  { key: "Mod-i", run: wrap("*") },
  { key: "Mod-Shift-x", run: wrap("~~") },
  { key: "Mod-e", run: wrap("`") },
  { key: "Mod-k", run: link },
];

/**
 * One CodeMirror instance per document preserves undo, selection and scroll across ordinary saves;
 * switching between live preview and source reconfigures it instead of creating a new editor.
 */
export function NoteEditor({ vaultId, noteId, value, source, readOnly, onChange, onFiles, onReady }: {
  vaultId: string; noteId: string; value: string; source: boolean; readOnly: boolean;
  onChange: (value: string) => void; onFiles: (files: File[], insert: (text: string) => void) => void;
  onReady: (insert: ((text: string) => void) | null) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<EditorView | null>(null);
  const mode = useRef(new Compartment());
  const applied = useRef(source);
  const callbacks = useRef({ onChange, onFiles, onReady });
  useEffect(() => { callbacks.current = { onChange, onFiles, onReady }; }, [onChange, onFiles, onReady]);
  const latest = useRef({ value, source });
  useEffect(() => { latest.current = { value, source }; }, [value, source]);
  useEffect(() => {
    if (!host.current) return;
    const insert = (text: string) => {
      const view = editor.current;
      if (view && !view.state.readOnly) view.dispatch(view.state.replaceSelection(text));
    };
    const { value: initial, source: initialSource } = latest.current;
    applied.current = initialSource;
    const view = new EditorView({ parent: host.current, state: EditorState.create({
      doc: initial,
      selection: { anchor: initialSource ? 0 : (frontmatter(initial)?.to ?? 0) },
      extensions: [theme, EditorView.lineWrapping, history(), markdown({ base: markdownLanguage }), syntaxHighlighting(defaultHighlightStyle), highlightSelectionMatches(),
        keymap.of([...formatKeymap, indentWithTab, ...defaultKeymap, ...historyKeymap, ...searchKeymap]), EditorState.readOnly.of(readOnly),
        mode.current.of(initialSource ? [] : [liveHeadings, livePreview(vaultId, noteId)]),
        EditorView.updateListener.of((update) => { if (update.docChanged && !update.transactions.some((tr) => tr.annotation(Transaction.addToHistory) === false)) callbacks.current.onChange(update.state.doc.toString()); }),
        EditorView.domEventHandlers({ paste: (event) => {
          const files = Array.from(event.clipboardData?.files ?? []);
          if (!files.length || readOnly) return false;
          event.preventDefault(); callbacks.current.onFiles(files, insert); return true;
        } }),
      ],
    }) });
    editor.current = view; callbacks.current.onReady(insert);
    return () => { callbacks.current.onReady(null); editor.current = null; view.destroy(); };
  }, [vaultId, noteId, readOnly]);
  useEffect(() => {
    if (applied.current === source) return;
    applied.current = source;
    editor.current?.dispatch({ effects: mode.current.reconfigure(source ? [] : [liveHeadings, livePreview(vaultId, noteId)]) });
  }, [source, vaultId, noteId]);
  useEffect(() => {
    const view = editor.current;
    if (!view || view.composing || view.state.doc.toString() === value) return;
    const anchor = Math.min(view.state.selection.main.anchor, value.length);
    const head = Math.min(view.state.selection.main.head, value.length);
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value }, selection: { anchor, head }, annotations: Transaction.addToHistory.of(false) });
  }, [value]);
  return <div ref={host} className="h-full min-h-0" />;
}
