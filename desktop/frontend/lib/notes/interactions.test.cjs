/* eslint-disable @typescript-eslint/no-require-imports -- Standalone Node regression harness. */
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const ts = require("typescript");

/** Load dependency-free TypeScript helpers without executing the desktop app. */
function load(name) {
  const output = ts.transpileModule(fs.readFileSync(path.join(__dirname, `${name}.ts`), "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText;
  const mod = { exports: {} }; vm.runInNewContext(output, { module: mod, exports: mod.exports }); return mod.exports;
}

test("tree drag rejects cycles, cross-vault moves, stale trash and no-op parents", () => {
  const { canDropNote } = load("tree-drag");
  const folder = { vaultId: "v", id: "a", parentId: "", directory: true };
  assert.equal(canDropNote(folder, "v", "b", ["b"], false), true);
  assert.equal(canDropNote(folder, "v", "a", ["a"], false), false);
  assert.equal(canDropNote(folder, "v", "child", ["a", "child"], false), false);
  assert.equal(canDropNote(folder, "v", "", [], false), false);
  assert.equal(canDropNote(folder, "other", "b", [], false), false);
  assert.equal(canDropNote(folder, "v", "b", [], true), false);
  assert.equal(canDropNote({ ...folder, deleted: true }, "v", "b", [], false), false);
  assert.equal(canDropNote(null, "v", "b", [], false), false);
  assert.equal(canDropNote({ ...folder, parentId: "b" }, "v", "", [], false), true);
});

test("BR conversion does not enable HTML or modify code", () => {
  const { remarkNoteBreaks } = load("render-markdown");
  const tree = { type: "root", children: [{ type: "html", value: "<br>" }, { type: "html", value: "<BR />" }, { type: "html", value: '<img onerror="bad()">' }, { type: "code", value: "<br>" }] };
  remarkNoteBreaks()(tree);
  assert.equal(tree.children[0].type, "break"); assert.equal(tree.children[1].type, "break");
  assert.equal(tree.children[2].type, "html"); assert.equal(tree.children[3].value, "<br>");
});

test("live range filters rendered HAST and emits the global footnote section only once", () => {
  const { rangeChildren } = load("render-markdown");
  const node = (tagName, from, to) => ({ type: "element", tagName, position: { start: { offset: from }, end: { offset: to } } });
  const footnotes = { type: "element", tagName: "section", properties: { dataFootnotes: true } };
  const children = [node("h1", 0, 5), node("table", 8, 80), node("p", 82, 99), footnotes];
  assert.equal(rangeChildren(children, { from: 8, to: 80 }).map((item) => item.tagName).join(","), "table");
  const notes = { children: rangeChildren(children, { from: 99, to: 99, footnotesOnly: true }) };
  assert.equal(notes.children.length, 1); assert.equal(notes.children[0], footnotes);
});

test("task toggles flip only the marker of the clicked list item", () => {
  const { toggleTask } = load("live-editing");
  const text = "- [ ] one\n  1. [x] two\n- plain\n";
  assert.equal(JSON.stringify(toggleTask(text, 0)), JSON.stringify({ from: 3, to: 4, insert: "x" }));
  assert.equal(JSON.stringify(toggleTask(text, text.indexOf("1.") - 2)), JSON.stringify({ from: text.indexOf("[x]") + 1, to: text.indexOf("[x]") + 2, insert: " " }));
  assert.equal(toggleTask(text, text.indexOf("- plain")), null);
});

test("click caret mapping skips Markdown syntax between rendered characters", () => {
  const { caretOffset } = load("live-editing");
  assert.equal(caretOffset("## Title here", "Title"), 8);
  assert.equal(caretOffset("a **bold** [link](https://x.y) end", "a bold li"), 14);
  assert.equal(caretOffset("plain", ""), 0);
});

test("note info prefers front matter creation time and counts Chinese per character", () => {
  const output = ts.transpileModule(fs.readFileSync(path.join(__dirname, "note-info.ts"), "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText;
  const mod = { exports: {} };
  const { frontmatter } = { frontmatter: (text) => text.startsWith("---\ncreated: 2024-01-02 03:04\n---\n") ? { to: 33, rows: [["created", "2024-01-02 03:04"]] } : null };
  vm.runInNewContext(output, { module: mod, exports: mod.exports, require: () => ({ frontmatter }) });
  const info = mod.exports.noteInfo("---\ncreated: 2024-01-02 03:04\n---\n今日主题 hello world", 5, 9);
  assert.equal(info.created, new Date(2024, 0, 2, 3, 4).getTime());
  assert.equal(info.words, 6);
  assert.equal(mod.exports.noteInfo("plain", 5, 9).created, 5);
});
