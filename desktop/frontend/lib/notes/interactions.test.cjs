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
  const { rehypeNoteRange } = load("render-markdown");
  const node = (tagName, from, to) => ({ type: "element", tagName, position: { start: { offset: from }, end: { offset: to } } });
  const footnotes = { type: "element", tagName: "section", properties: { dataFootnotes: true } };
  const tree = { type: "root", children: [node("h1", 0, 5), node("table", 8, 80), node("p", 82, 99), footnotes] };
  const notes = { type: "root", children: [...tree.children] };
  rehypeNoteRange({ from: 8, to: 80 })(tree);
  assert.equal(tree.children.map((item) => item.tagName).join(","), "table");
  rehypeNoteRange({ from: 99, to: 99, footnotesOnly: true })(notes);
  assert.equal(notes.children.length, 1); assert.equal(notes.children[0], footnotes);
});
