/* eslint-disable @typescript-eslint/no-require-imports -- Node CJS test harness mirrors the existing i18n tests and loads TS without a browser. */
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const ts = require("typescript");
const { createRequire } = require("node:module");

/** Load pure parser helpers without a browser or Wails instance. */
function load(name) {
  const file = path.join(__dirname, `${name}.ts`);
  const output = ts.transpileModule(fs.readFileSync(file, "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true } }).outputText;
  const mod = { exports: {} }; const local = createRequire(file);
  vm.runInNewContext(output, { URLSearchParams, module: mod, exports: mod.exports, require: (id) => id.startsWith("@/lib/notes/") ? load(id.slice("@/lib/notes/".length)) : id === "@/lib/i18n" ? { t: (value) => value } : id === "@/components/notes/note-markdown" ? { NoteMarkdown: () => null, noteMarkdownClassName: "" } : local(id) });
  return mod.exports;
}

test("YAML properties preserve lists, dates, empty fields and original source", () => {
  const { frontmatter } = load("markdown");
  const text = '---\ntitle: "Example"\nauthor:\n  - "[[Author]]"\ncreated: 2026-10-08\npublished:\n---\n# Body\n';
  const metadata = frontmatter(text);
  assert.equal(metadata.error, false);
  assert.equal(text.slice(metadata.to), "# Body\n");
  assert.equal(metadata.rows.find(([key]) => key === "author")[1], "[[Author]]");
  assert.equal(metadata.rows.find(([key]) => key === "published")[1], "");
  assert.equal(frontmatter("---\nunfinished: true"), null);
});

test("invalid YAML remains visible as original text, never silently discarded", () => {
  const metadata = load("markdown").frontmatter("---\nvalue: [unfinished\n---\nbody");
  assert.equal(metadata.error, true); assert.match(metadata.raw, /unfinished/);
});

test("TOC excludes metadata and fenced code, handles nested and duplicate headings", () => {
  const text = "---\ntitle: test\n---\n# First\n\n```md\n# Not a heading\n```\n\n## Same\n\n## Same\n\nSetext\n------\n";
  const headings = load("headings").noteHeadings(text);
  assert.equal(headings.length, 4);
  assert.equal(headings.map((heading) => heading.level).join(","), "1,2,2,2");
  assert.equal(headings[1].text, "Same"); assert.notEqual(headings[1].from, headings[2].from);
});

test("image paths preserve spaces and Chinese; active schemes cannot load", () => {
  const { imageTarget } = load("markdown");
  const local = imageTarget("assets/截图 (1).png", "v", "n");
  assert.equal(new URL(local.src, "http://localhost").searchParams.get("path"), "assets/截图 (1).png");
  assert.equal(imageTarget("https://example.com/image.png", "v", "n").remote, true);
  for (const value of ["javascript:alert(1)", "file:///secret", "data:image/svg+xml,bad", "//example.com/a"]) assert.equal(imageTarget(value, "v", "n"), null);
});

test("live Markdown decorates YAML, standard and wiki images without changing original bytes", () => {
  const { EditorState } = require("@codemirror/state");
  const { markdown } = require("@codemirror/lang-markdown");
  const field = load("live-preview").livePreview("v", "n");
  const text = '---\ntitle: Test\n---\n# Body\n\n![Remote](https://example.com/a.png)\n\n![[截图 (1).png]]\n\n```\n![[Not-an-image.png]]\n```\n';
  const state = EditorState.create({ doc: text, selection: { anchor: text.length }, extensions: [markdown(), field] });
  let properties = 0, images = 0;
  state.field(field).between(0, text.length, (_from, _to, decoration) => {
    const widget = decoration.spec.widget;
    if (widget?.constructor.name !== "MarkdownWidget") return;
    const block = text.slice(widget.from, widget.to);
    if (block.startsWith("---")) properties++;
    if (block.includes("![Remote]") || block.includes("![[截图")) images++;
  });
  assert.equal(properties, 1); assert.equal(images, 2); assert.equal(state.doc.toString(), text);
});

test("code blocks preserve Markdown-looking content and expose an editable original", () => {
  const { EditorState } = require("@codemirror/state");
  const { markdown } = require("@codemirror/lang-markdown");
  const text = "Intro\n\n```md\n![[asset.png]]\n# literal heading\n```\n\n~~~go\nfmt.Println(1)\n~~~\n";
  const preview = load("live-preview");
  const field = preview.livePreview("v", "n");
  const state = EditorState.create({ doc: text, extensions: [markdown(), field] });
  const blocks = load("code-blocks").noteCodeBlocks(state);
  assert.equal(blocks.length, 2);
  assert.equal(blocks[0].code, "![[asset.png]]\n# literal heading");
  assert.equal(blocks[1].language, "go");
  let widgets = 0;
  state.field(field).between(0, text.length, (_a, _b, decoration) => {
    if (decoration.spec.widget?.constructor.name === "MarkdownWidget" && /^(?:```|~~~)/.test(text.slice(decoration.spec.widget.from, decoration.spec.widget.to))) widgets++;
  });
  assert.equal(widgets, 2);
  const edited = state.update({ selection: { anchor: blocks[0].from + 6 }, effects: preview.setFocus.of(true) }).state;
  let remaining = 0;
  edited.field(field).between(0, text.length, (_a, _b, decoration) => {
    if (decoration.spec.widget?.constructor.name === "MarkdownWidget" && /^(?:```|~~~)/.test(text.slice(decoration.spec.widget.from, decoration.spec.widget.to))) remaining++;
  });
  assert.equal(remaining, 1);
  assert.equal(edited.doc.toString(), text);
  // Losing focus renders every block again, even the one holding the caret.
  let blurred = 0;
  edited.update({ effects: preview.setFocus.of(false) }).state.field(field).between(0, text.length, (_a, _b, decoration) => {
    if (decoration.spec.widget?.constructor.name === "MarkdownWidget" && /^(?:```|~~~)/.test(text.slice(decoration.spec.widget.from, decoration.spec.widget.to))) blurred++;
  });
  assert.equal(blurred, 2);
});

test("Backspace and Delete at rendered block boundaries never remove neighboring prose", () => {
  const { EditorState } = require("@codemirror/state");
  const { EditorView } = require("@codemirror/view");
  const { markdown } = require("@codemirror/lang-markdown");
  const { deleteCharBackward, deleteCharForward } = require("@codemirror/commands");
  for (const [anchor, command] of [[5, deleteCharBackward], [3, deleteCharForward]]) {
    const field = load("live-preview").livePreview("v", "n");
    let state = EditorState.create({ doc: "AAA\n\nBBB", selection: { anchor }, extensions: [markdown(), field] });
    const view = Object.create(EditorView.prototype);
    Object.defineProperty(view, "state", { get: () => state });
    view.dispatch = (transaction) => { state = transaction.state; };
    assert.equal(state.facet(EditorView.atomicRanges).length, 0);
    assert.equal(command(view), true);
    assert.equal(state.doc.toString(), "AAA\nBBB");
  }
});

test("global footnotes remain outside an inclusive final block at document end", () => {
  const { EditorState } = require("@codemirror/state");
  const { markdown } = require("@codemirror/lang-markdown");
  const field = load("live-preview").livePreview("v", "n");
  const text = "Intro\n\nReference[^n]\n\n[^n]: Definition\n\nTail without newline";
  const state = EditorState.create({ doc: text, selection: { anchor: 0 }, extensions: [markdown(), field] });
  let count = 0;
  state.field(field).between(text.length, text.length, (from, to, decoration) => {
    if (from === to && decoration.spec.side === 1) count++;
  });
  assert.equal(count, 1);
});

test("reading wiki compatibility never rewrites code", () => {
  const { readingMarkdown } = load("reading-markdown");
  const text = "`![[inline.png]]`\n\n```md\n![[literal.png]]\n```\n\n![[actual.png]]\n\n![[file.pdf#page=2]]";
  const result = readingMarkdown(text);
  assert.ok(result.includes("`![[inline.png]]`"));
  assert.ok(result.includes("```md\n![[literal.png]]\n```"));
  assert.ok(result.includes("![actual.png](actual.png)"));
  assert.ok(result.includes("[file.pdf#page=2](file.pdf#page=2)"));
});

test("the block being edited renders list bullets, task boxes, headings and inline code from their symbols", () => {
  const { EditorState } = require("@codemirror/state");
  const { markdown, markdownLanguage } = require("@codemirror/lang-markdown");
  const preview = load("live-preview");
  const field = preview.livePreview("v", "n");
  const text = "# Title\n\n- [x] done\n- item with `code`\n\n> quote\n\n---\n";
  // The caret sits on a later, separate block so none of these lines is touched by it.
  const base = EditorState.create({ doc: text + "tail", extensions: [markdown({ base: markdownLanguage }), field] });
  const state = base.update({ selection: { anchor: base.doc.length }, effects: preview.setFocus.of(true) }).state;
  const classes = [], widgets = [];
  state.field(field).between(0, state.doc.length, (_a, _b, d) => { if (d.spec.class) classes.push(d.spec.class); if (d.spec.widget) widgets.push(d.spec.widget.constructor.name); });
  // Focused with the caret in "tail": every earlier block stays a rendered widget, nothing is hidden by hand.
  assert.ok(widgets.filter((w) => w === "MarkdownWidget").length >= 4);
  // Caret inside the list block: it is source, bullets/boxes/code are decorated.
  const inList = state.update({ selection: { anchor: text.indexOf("item") } }).state;
  const kinds = [];
  inList.field(field).between(0, inList.doc.length, (_a, _b, d) => { if (d.spec.class) kinds.push(d.spec.class); if (d.spec.widget) kinds.push(d.spec.widget.constructor.name); });
  assert.ok(kinds.includes("TaskBoxWidget")); assert.ok(kinds.includes("cm-note-inline-code"));
});
