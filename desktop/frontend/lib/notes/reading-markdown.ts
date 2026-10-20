import { markdownLanguage } from "@codemirror/lang-markdown";

interface Rewrite { from: number; to: number; renderedFrom: number; renderedTo: number }
export interface PreparedReadingMarkdown { text: string; toRendered: (offset: number) => number; toSource: (offset: number) => number }
const recent: { source: string; value: PreparedReadingMarkdown }[] = [];

/** Display-only wiki rewrites retain offset maps so live blocks and anchor jumps use original source. */
export function prepareReadingMarkdown(text: string): PreparedReadingMarkdown {
  const cached = recent.find((item) => item.source === text);
  if (cached) return cached.value;
  const tree = markdownLanguage.parser.parse(text);
  const rewrites: Rewrite[] = [];
  let delta = 0;
  const rendered = text.replace(/(!?)\[\[([^\]\n]+)\]\]/g, (all, embed: string, body: string, offset: number) => {
    let node = tree.resolveInner(offset, 1);
    while (node) {
      if (["FencedCode", "CodeBlock", "InlineCode"].includes(node.name)) return all;
      if (!node.parent) break; node = node.parent;
    }
    const [target, label] = body.split("|");
    const prefix = /\.pdf(?:#.*)?$/i.test(target) ? "" : embed;
    const replacement = `${prefix}[${(label || target).replace(/[\[\]]/g, "")}](${encodeURI(target).replace(/\(/g, "%28").replace(/\)/g, "%29")})`;
    rewrites.push({ from: offset, to: offset + all.length, renderedFrom: offset + delta, renderedTo: offset + delta + replacement.length });
    delta += replacement.length - all.length;
    return replacement;
  });
  /** Positions inside a compatibility token resolve to its start; block boundaries remain exact. */
  const toRendered = (offset: number) => {
    let delta = 0;
    for (const edit of rewrites) {
      if (offset < edit.from) break;
      if (offset < edit.to) return edit.renderedFrom;
      delta += edit.renderedTo - edit.renderedFrom - (edit.to - edit.from);
    }
    return offset + delta;
  };
  /** Reverse the display-only length changes before passing a destination to CodeMirror. */
  const toSource = (offset: number) => {
    let delta = 0;
    for (const edit of rewrites) {
      if (offset < edit.renderedFrom) break;
      if (offset < edit.renderedTo) return edit.from;
      delta += edit.renderedTo - edit.renderedFrom - (edit.to - edit.from);
    }
    return offset - delta;
  };
  const value = { text: rendered, toRendered, toSource };
  recent.unshift({ source: text, value }); recent.splice(2);
  return value;
}

/** Compatibility rewrites are render-only and never modify inline or fenced code. */
export function readingMarkdown(text: string): string {
  return prepareReadingMarkdown(text).text;
}
