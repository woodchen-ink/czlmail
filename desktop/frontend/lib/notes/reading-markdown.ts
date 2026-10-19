import { markdownLanguage } from "@codemirror/lang-markdown";

/** Compatibility rewrites are display-only and never touch inline or fenced code. */
export function readingMarkdown(text: string): string {
  const tree = markdownLanguage.parser.parse(text);
  return text.replace(/(!?)\[\[([^\]\n]+)\]\]/g, (all, embed: string, body: string, offset: number) => {
    let node = tree.resolveInner(offset, 1);
    while (node) {
      if (["FencedCode", "CodeBlock", "InlineCode"].includes(node.name)) return all;
      if (!node.parent) break; node = node.parent;
    }
    const [target, label] = body.split("|");
    const prefix = /\.pdf(?:#.*)?$/i.test(target) ? "" : embed;
    return `${prefix}[${(label || target).replace(/[\[\]]/g, "")}](${encodeURI(target).replace(/\(/g, "%28").replace(/\)/g, "%29")})`;
  });
}
