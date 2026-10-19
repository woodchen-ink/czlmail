import { EditorState, StateField } from "@codemirror/state";
import { Decoration, EditorView, WidgetType, type DecorationSet } from "@codemirror/view";
import { syntaxTree } from "@codemirror/language";
import { frontmatter, imageTarget, type Frontmatter } from "@/lib/notes/markdown";
import { t } from "@/lib/i18n";
import { noteCodeBlocks, type NoteCodeBlock } from "@/lib/notes/code-blocks";

/** Readable code outside the active block; clicking Edit reveals the original fenced source. */
class CodeWidget extends WidgetType {
  constructor(readonly block: NoteCodeBlock) { super(); }
  eq(other: CodeWidget) { return this.block.code === other.block.code && this.block.language === other.block.language && this.block.from === other.block.from; }
  toDOM(view: EditorView) {
    const container = document.createElement("div"); container.className = "bg-muted my-3 overflow-hidden rounded-md";
    const header = container.appendChild(document.createElement("div")); header.className = "text-muted-foreground flex items-center gap-3 px-4 py-2 text-sm";
    const language = header.appendChild(document.createElement("span")); language.textContent = this.block.language || "Code"; language.className = "mr-auto";
    const copy = header.appendChild(document.createElement("button")); copy.type = "button"; copy.textContent = t("复制");
    copy.onclick = () => { void navigator.clipboard.writeText(this.block.code).then(() => { copy.textContent = t("已复制"); }).catch(() => { copy.textContent = t("复制失败，请手动选择代码"); }); };
    const edit = header.appendChild(document.createElement("button")); edit.type = "button"; edit.textContent = t("编辑");
    edit.onclick = () => { view.dispatch({ selection: { anchor: this.block.from } }); view.focus(); };
    const pre = container.appendChild(document.createElement("pre")); pre.className = "overflow-x-auto px-4 pb-4 font-mono text-sm"; pre.style.whiteSpace = "pre"; pre.style.lineHeight = "1.6";
    pre.appendChild(document.createElement("code")).textContent = this.block.code;
    return container;
  }
}

class LinkWidget extends WidgetType {
  constructor(readonly target: string, readonly label: string) { super(); }
  eq(other: LinkWidget) { return this.target === other.target && this.label === other.label; }
  toDOM() { const link = document.createElement("a"); link.href = "#"; link.dataset.noteLink = this.target; link.textContent = this.label; link.className = "text-primary cursor-pointer underline"; return link; }
}

/** A real block widget may change height; StateField supplies it before viewport measurement. */
class MetadataWidget extends WidgetType {
  constructor(readonly value: Frontmatter) { super(); }
  eq(other: MetadataWidget) { return other.value.raw === this.value.raw; }
  toDOM() {
    const details = document.createElement("details");
    details.className = "text-muted-foreground my-3 text-sm";
    const summary = details.appendChild(document.createElement("summary")); summary.textContent = t("属性"); summary.style.cursor = "pointer";
    if (this.value.error) { const pre = details.appendChild(document.createElement("pre")); pre.textContent = this.value.raw; }
    else for (const [key, value] of this.value.rows) {
      const row = details.appendChild(document.createElement("div")); row.className = "grid grid-cols-[8rem_1fr] gap-3 py-1";
      row.appendChild(document.createElement("span")).textContent = key;
      const cell = row.appendChild(document.createElement("span")); cell.textContent = value; cell.className = "break-words whitespace-pre-wrap";
    }
    return details;
  }
}

/** Explicit loading protects remote-image privacy while local attachments display immediately. */
class ImageWidget extends WidgetType {
  constructor(readonly src: string, readonly label: string, readonly remote: boolean) { super(); }
  eq(other: ImageWidget) { return this.src === other.src && this.label === other.label; }
  toDOM() {
    const wrapper = document.createElement("span");
    const load = () => {
      wrapper.replaceChildren();
      const image = wrapper.appendChild(document.createElement("img"));
      if (!this.remote) { try { image.dataset.notePath = new URL(this.src, "http://localhost").searchParams.get("path") ?? ""; } catch { /* Invalid paths are rejected by the backend. */ } }
      image.referrerPolicy = "no-referrer"; image.alt = this.label; image.loading = "lazy";
      image.style.maxWidth = "100%"; image.style.maxHeight = "360px"; image.style.objectFit = "contain";
      image.onerror = () => button(t("图片加载失败，点击重试")); image.src = this.src;
    };
    const button = (label: string) => {
      wrapper.replaceChildren(); const control = wrapper.appendChild(document.createElement("button"));
      control.type = "button"; control.className = "bg-muted text-muted-foreground rounded-md px-3 py-2 text-sm";
      control.textContent = `${label} · ${this.label}`; control.onclick = (event) => { event.preventDefault(); load(); };
    };
    if (this.remote) button(t("点击加载远程图片")); else load();
    return wrapper;
  }
}

/** Render parsed Markdown nodes; metadata and wiki image support never rewrite document bytes. */
export function livePreview(vaultId: string, noteId: string) {
  const build = (state: EditorState): DecorationSet => {
    const ranges: import("@codemirror/state").Range<Decoration>[] = [];
    const text = state.doc.toString(); const metadata = frontmatter(text);
    if (metadata) ranges.push(Decoration.replace({ widget: new MetadataWidget(metadata), block: true }).range(0, metadata.to));
    const active = state.doc.lineAt(state.selection.main.head);
    const codeBlocks = noteCodeBlocks(state);
    for (const block of codeBlocks) {
      const editing = state.selection.ranges.some((selection) => selection.from <= block.to && selection.to >= block.from);
      if (!editing) ranges.push(Decoration.replace({ widget: new CodeWidget(block), block: true }).range(block.from, block.to));
      else for (let line = state.doc.lineAt(block.from); line.from <= block.to;) {
        ranges.push(Decoration.line({ class: "cm-note-code-line" }).range(line.from));
        if (line.to >= state.doc.length) break; line = state.doc.lineAt(line.to + 1);
      }
    }
    const image = (from: number, to: number, src: string, label: string) => {
      const target = imageTarget(src, vaultId, noteId);
      if (target) ranges.push(Decoration.replace({ widget: new ImageWidget(target.src, label, target.remote) }).range(from, to));
    };
    syntaxTree(state).iterate({ enter(node) {
      if (metadata && node.from < metadata.to) return node.to <= metadata.to ? false : undefined;
      const line = state.doc.lineAt(node.from);
      if (node.name.startsWith("ATXHeading")) ranges.push(Decoration.line({ class: `cm-note-h${node.name.slice(-1)}` }).range(line.from));
      if (node.name === "Image") {
        let src = ""; let child = node.node.firstChild;
        while (child) { if (child.name === "URL") src = text.slice(child.from, child.to); child = child.nextSibling; }
        if (src) image(node.from, node.to, src.replace(/^<|>$/g, ""), text.slice(node.from, node.to).match(/^!\[([^\]]*)\]/)?.[1] ?? "");
        return false;
      }
      if (node.name === "Link" && line.from !== active.from) {
        let child = node.node.firstChild; let url = "";
        while (child) { if (child.name === "URL") url = text.slice(child.from, child.to).replace(/^<|>$/g, ""); child = child.nextSibling; }
        const label = text.slice(node.from, node.to).match(/^\[([^\]]*)\]/)?.[1];
        if (url && label) ranges.push(Decoration.replace({ widget: new LinkWidget(url, label) }).range(node.from, node.to));
        return false;
      }
      if (line.from !== active.from && ["HeaderMark", "EmphasisMark", "CodeMark"].includes(node.name) && node.to > node.from) ranges.push(Decoration.replace({}).range(node.from, node.to));
      if (node.name === "FencedCode" || node.name === "CodeBlock" || node.name === "InlineCode") return false;
    } });
    // Wiki embeds are an explicit compatibility extension, excluded from code and metadata.
    for (const match of text.matchAll(/!\[\[([^\]\n]+)\]\]/g)) {
      const from = match.index; if (metadata && from < metadata.to) continue;
      let parent = syntaxTree(state).resolveInner(from, 1); let code = false;
      while (parent.parent) { if (["FencedCode", "CodeBlock", "InlineCode"].includes(parent.name)) code = true; parent = parent.parent; }
      if (code || ranges.some((range) => range.from <= from && range.to > from)) continue;
      const [src, label] = match[1].split("|");
      if (/\.pdf(?:#.*)?$/i.test(src)) ranges.push(Decoration.replace({ widget: new LinkWidget(src, label || src) }).range(from, from + match[0].length));
      else image(from, from + match[0].length, src, label || src);
    }
    return Decoration.set(ranges, true);
  };
  return StateField.define<DecorationSet>({
    create: build,
    update: (value, transaction) => transaction.docChanged || transaction.selection || syntaxTree(transaction.startState) !== syntaxTree(transaction.state) ? build(transaction.state) : value,
    provide: (field) => [EditorView.decorations.from(field), EditorView.atomicRanges.of((view) => view.state.field(field))],
  });
}
