import { markdownLanguage } from "@codemirror/lang-markdown";
import { frontmatter } from "@/lib/notes/markdown";

export interface NoteHeading { from: number; level: number; text: string }

/** Use the Markdown parser rather than line matching so fenced code and YAML are excluded. */
export function noteHeadings(text: string): NoteHeading[] {
  const metadata = frontmatter(text);
  const headings: NoteHeading[] = [];
  markdownLanguage.parser.parse(text).iterate({ enter(node) {
    if (metadata && node.from < metadata.to) return node.to <= metadata.to ? false : undefined;
    if (/^(ATXHeading|SetextHeading)[1-6]$/.test(node.name)) {
      const content = text.slice(node.from, node.to).replace(/^#{1,6}\s+/, "").replace(/\s+#+\s*$/, "").replace(/\r?\n[=-]+\s*$/, "");
      headings.push({ from: node.from, level: Number(node.name.slice(-1)), text: content.replace(/!?(?:\[([^\]]*)\])\([^)]*\)/g, "$1").replace(/[*_`~]/g, "").trim() });
      return false;
    }
    if (["FencedCode", "CodeBlock"].includes(node.name)) return false;
  } });
  return headings;
}
