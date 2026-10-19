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
  vm.runInNewContext(output, { URLSearchParams, module: mod, exports: mod.exports, require: (id) => id.startsWith("@/lib/notes/") ? load(id.slice("@/lib/notes/".length)) : id === "@/lib/i18n" ? { t: (value) => value } : local(id) });
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
  const state = EditorState.create({ doc: text, extensions: [markdown(), field] });
  let properties = 0, images = 0;
  state.field(field).between(0, text.length, (_from, _to, decoration) => {
    if (decoration.spec.widget?.constructor.name === "MetadataWidget") properties++;
    if (decoration.spec.widget?.constructor.name === "ImageWidget") images++;
  });
  assert.equal(properties, 1); assert.equal(images, 2); assert.equal(state.doc.toString(), text);
});

test("code blocks preserve Markdown-looking content and expose an editable original", () => {
  const { EditorState } = require("@codemirror/state");
  const { markdown } = require("@codemirror/lang-markdown");
  const text = "Intro\n\n```md\n![[asset.png]]\n# literal heading\n```\n\n~~~go\nfmt.Println(1)\n~~~\n";
  const field = load("live-preview").livePreview("v", "n");
  const state = EditorState.create({ doc: text, extensions: [markdown(), field] });
  const blocks = load("code-blocks").noteCodeBlocks(state);
  assert.equal(blocks.length, 2);
  assert.equal(blocks[0].code, "![[asset.png]]\n# literal heading");
  assert.equal(blocks[1].language, "go");
  let widgets = 0;
  state.field(field).between(0, text.length, (_a, _b, decoration) => {
    if (decoration.spec.widget?.constructor.name === "CodeWidget") widgets++;
  });
  assert.equal(widgets, 2);
  const edited = state.update({ selection: { anchor: blocks[0].from + 6 } }).state;
  let remaining = 0;
  edited.field(field).between(0, text.length, (_a, _b, decoration) => {
    if (decoration.spec.widget?.constructor.name === "CodeWidget") remaining++;
  });
  assert.equal(remaining, 1);
  assert.equal(edited.doc.toString(), text);
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
