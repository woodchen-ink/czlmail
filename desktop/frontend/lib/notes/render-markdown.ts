export interface MarkdownNode {
  type: string;
  value?: string;
  children?: MarkdownNode[];
  tagName?: string;
  properties?: Record<string, unknown>;
  position?: { start: { offset?: number }; end: { offset?: number } };
}
export interface MarkdownRange { from: number; to: number; footnotesOnly?: boolean }

/** Admit BR only; a BR-led HTML block keeps its remaining prose as safe text, never executable HTML. */
export function remarkNoteBreaks() {
  return (tree: MarkdownNode) => {
    /** Inline BR becomes a break; BR-led block text is preserved rather than swallowed by skipHtml. */
    function visit(node: MarkdownNode) {
      if (node.type === "html" && /^\s*<br\s*\/?\s*>/i.test(node.value ?? "")) {
        const value = node.value ?? "";
        if (/^\s*<br\s*\/?\s*>\s*$/i.test(value)) node.type = "break";
        else {
          node.type = "paragraph";
          node.children = value.split(/(<br\s*\/?\s*>)/i).filter(Boolean).map((part) => /^<br\s*\/?\s*>$/i.test(part) ? { type: "break" } : { type: "text", value: part });
        }
        delete node.value;
      }
      node.children?.forEach(visit);
    }
    visit(tree);
  };
}

/** Preserve source destinations on generated anchors before the live viewport drops other blocks. */
export function rehypeNoteAnchors() {
  return (tree: MarkdownNode) => {
    const positions = new Map<string, number>();
    /** Collect original HAST IDs, including generated footnote references and definition list items. */
    function collect(node: MarkdownNode) {
      const id = node.properties?.id;
      const offset = node.position?.start.offset;
      if (typeof id === "string" && typeof offset === "number") positions.set(id, offset);
      node.children?.forEach(collect);
    }
    /** Attach only destinations produced by this renderer, never an arbitrary filesystem target. */
    function annotate(node: MarkdownNode) {
      const href = node.properties?.href;
      if (node.tagName === "a" && typeof href === "string" && href.startsWith("#")) {
        const destination = positions.get(href.slice(1));
        if (destination !== undefined && node.properties) node.properties.dataNoteAnchorPosition = destination;
      }
      node.children?.forEach(annotate);
    }
    collect(tree); annotate(tree);
  };
}

/** Top-level HAST children overlapping a rendered range; the footnote section renders once, on its own. */
export function rangeChildren<T extends MarkdownNode>(children: T[], range: MarkdownRange): T[] {
  return children.filter((node) => {
    const footnotes = node.tagName === "section" && node.properties?.dataFootnotes !== undefined;
    if (range.footnotesOnly) return footnotes;
    return !footnotes && (node.position?.start.offset ?? -1) < range.to && (node.position?.end.offset ?? -1) > range.from;
  });
}
