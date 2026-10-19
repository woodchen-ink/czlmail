import { syntaxTree } from "@codemirror/language";
import type { EditorState } from "@codemirror/state";

export interface NoteCodeBlock { from: number; to: number; code: string; language: string }

/** Extract parsed fenced/indented code without interpreting its contents as Markdown. */
export function noteCodeBlocks(state: EditorState): NoteCodeBlock[] {
  const result: NoteCodeBlock[] = [];
  syntaxTree(state).iterate({ enter(node) {
    if (node.name !== "FencedCode" && node.name !== "CodeBlock") return;
    const raw = state.doc.sliceString(node.from, node.to);
    if (node.name === "CodeBlock") {
      result.push({ from: node.from, to: node.to, language: "", code: raw.replace(/^(?: {4}|\t)/gm, "") });
    } else {
      const lines = raw.split("\n");
      const opening = /^\s*(`{3,}|~{3,})(.*)$/.exec(lines[0]);
      const language = opening?.[2].trim().split(/\s+/)[0] ?? "";
      lines.shift();
      if (opening && lines.length && new RegExp(`^\\s*${opening[1][0]}{${opening[1].length},}\\s*$`).test(lines[lines.length - 1])) lines.pop();
      result.push({ from: node.from, to: node.to, language, code: lines.join("\n") });
    }
    return false;
  } });
  return result;
}
