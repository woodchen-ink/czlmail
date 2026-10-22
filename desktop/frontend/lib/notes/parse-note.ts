import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkRehype from "remark-rehype";
import { visit } from "unist-util-visit";
import { urlAttributes } from "html-url-attributes";
import { defaultUrlTransform } from "react-markdown";
import type { Root, RootContent } from "hast";
import { frontmatter, type Frontmatter } from "@/lib/notes/markdown";
import { prepareReadingMarkdown, type PreparedReadingMarkdown } from "@/lib/notes/reading-markdown";
import { rangeChildren, remarkNoteBreaks, rehypeNoteAnchors, type MarkdownNode, type MarkdownRange } from "@/lib/notes/render-markdown";

/** One parsed document: every live block and the reader slice this tree instead of parsing again. */
export interface ParsedNote { metadata: Frontmatter | null; offset: number; prepared: PreparedReadingMarkdown; tree: Root }

const processor = unified().use(remarkParse).use(remarkGfm).use(remarkNoteBreaks).use(remarkRehype).use(rehypeNoteAnchors);
const recent: { text: string; value: ParsedNote }[] = [];

/** Parse once per document version; the live editor asks for the same text from every block. */
export function parseNote(text: string): ParsedNote {
  const cached = recent.find((item) => item.text === text);
  if (cached) return cached.value;
  const metadata = frontmatter(text);
  const offset = metadata?.to ?? 0;
  const prepared = prepareReadingMarkdown(text.slice(offset));
  const tree = processor.runSync(processor.parse(prepared.text)) as Root;
  sanitize(tree, (rendered) => offset + prepared.toSource(rendered));
  const value = { metadata, offset, prepared, tree };
  recent.unshift({ text, value }); recent.splice(3);
  return value;
}

/** Same as react-markdown's skipHtml + urlTransform, plus source offsets for caret placement and task toggles. */
function sanitize(tree: Root, toSource: (rendered: number) => number) {
  visit(tree, (node, index, parent) => {
    if (node.type === "raw" && parent && typeof index === "number") { parent.children.splice(index, 1); return index; }
    if (node.type !== "element") return;
    for (const key of Object.keys(urlAttributes)) {
      const allowed = urlAttributes[key];
      if (Object.hasOwn(node.properties, key) && (allowed === null || allowed.includes(node.tagName))) node.properties[key] = defaultUrlTransform(String(node.properties[key] ?? ""));
    }
    const start = node.position?.start.offset, end = node.position?.end.offset;
    if (typeof start === "number" && typeof end === "number") { node.properties.dataNoteFrom = toSource(start); node.properties.dataNoteTo = toSource(end); }
  });
}

/** Top-level children overlapping a source range; footnotes render once, in their own trailing block. */
export function sliceNote(parsed: ParsedNote, range?: MarkdownRange): Root {
  if (!range) return parsed.tree;
  const from = parsed.prepared.toRendered(Math.max(0, range.from - parsed.offset));
  const to = parsed.prepared.toRendered(Math.max(0, range.to - parsed.offset));
  const children = rangeChildren(parsed.tree.children as (RootContent & MarkdownNode)[], { from, to, footnotesOnly: range.footnotesOnly });
  return { type: "root", children };
}
