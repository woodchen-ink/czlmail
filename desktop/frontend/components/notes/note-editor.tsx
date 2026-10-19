"use client";

import { useEffect, useRef } from "react";
import { EditorState, Transaction } from "@codemirror/state";
import { EditorView, keymap } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { defaultHighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { searchKeymap, highlightSelectionMatches } from "@codemirror/search";
import { livePreview } from "@/lib/notes/live-preview";
import { frontmatter } from "@/lib/notes/markdown";

const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "transparent", color: "var(--foreground)" },
  ".cm-scroller": { overflow: "auto", fontFamily: "inherit", fontSize: "16px", lineHeight: "1.8" },
  ".cm-content": { padding: "24px 32px 160px", maxWidth: "960px", margin: "0 auto", width: "100%" },
  "&.cm-focused": { outline: "none", border: "none", boxShadow: "none" },
  ".cm-content:focus, .cm-content:focus-visible, .cm-scroller:focus-visible": { outline: "none", boxShadow: "none" },
  ".cm-cursor": { borderLeftColor: "var(--foreground)" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground": { backgroundColor: "var(--accent)" },
  ".cm-note-h1": { fontSize: "2em", fontWeight: "700", lineHeight: "1.4", margin: "0.5em 0" },
  ".cm-note-h2": { fontSize: "1.5em", fontWeight: "650", margin: "0.4em 0" },
  ".cm-note-h3, .cm-note-h4, .cm-note-h5, .cm-note-h6": { fontSize: "1.15em", fontWeight: "600" },
  ".cm-note-code-line": { backgroundColor: "var(--muted)", fontFamily: "monospace", fontSize: "14px", whiteSpace: "pre", paddingLeft: "16px", paddingRight: "16px" },
  ".cm-panels": { backgroundColor: "var(--card)", color: "var(--foreground)" },
});

/** One CodeMirror instance per document preserves undo, selection and scroll across ordinary saves. */
export function NoteEditor({ vaultId, noteId, value, source, readOnly, onChange, onFiles, onReady }: {
  vaultId: string; noteId: string; value: string; source: boolean; readOnly: boolean;
  onChange: (value: string) => void; onFiles: (files: File[], insert: (text: string) => void) => void;
  onReady: (insert: ((text: string) => void) | null) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<EditorView | null>(null);
  const callbacks = useRef({ onChange, onFiles, onReady });
  useEffect(() => { callbacks.current = { onChange, onFiles, onReady }; }, [onChange, onFiles, onReady]);
  const latest = useRef(value);
  useEffect(() => { latest.current = value; }, [value]);
  useEffect(() => {
    if (!host.current) return;
    const insert = (text: string) => {
      const view = editor.current;
      if (view && !view.state.readOnly) view.dispatch(view.state.replaceSelection(text));
    };
    const view = new EditorView({ parent: host.current, state: EditorState.create({
      doc: latest.current,
      selection: { anchor: source ? 0 : (frontmatter(latest.current)?.to ?? 0) },
      extensions: [theme, EditorView.lineWrapping, history(), markdown({ base: markdownLanguage }), syntaxHighlighting(defaultHighlightStyle), highlightSelectionMatches(),
        keymap.of([...defaultKeymap, ...historyKeymap, ...searchKeymap]), EditorState.readOnly.of(readOnly),
        ...(source ? [] : [livePreview(vaultId, noteId)]),
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
  }, [vaultId, noteId, source, readOnly]);
  useEffect(() => {
    const view = editor.current;
    if (!view || view.composing || view.state.doc.toString() === value) return;
    const anchor = Math.min(view.state.selection.main.anchor, value.length);
    const head = Math.min(view.state.selection.main.head, value.length);
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value }, selection: { anchor, head }, annotations: Transaction.addToHistory.of(false) });
  }, [value]);
  return <div ref={host} className="h-full min-h-0" />;
}
