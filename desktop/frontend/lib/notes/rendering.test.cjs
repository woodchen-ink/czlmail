/* eslint-disable @typescript-eslint/no-require-imports -- Exercise production TSX with Node's real React renderer. */
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const ts = require("typescript");
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
const root = path.resolve(__dirname, "../..");
const cache = new Map();

/** Transpile production components but replace only desktop transport and locale/toast side effects. */
function load(relative) {
  const file = path.join(root, relative);
  if (cache.has(file)) return cache.get(file);
  const output = ts.transpileModule(fs.readFileSync(file, "utf8"), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX, esModuleInterop: true } }).outputText;
  const local = createRequire(file); const mod = { exports: {} };
  const mocks = {
    "@/components/locale-provider": { useI18n: () => {} },
    "@/lib/i18n": { t: (value) => value },
    sonner: { toast: { success: () => {}, error: () => {} } },
  };
  vm.runInNewContext(output, { module: mod, exports: mod.exports, URLSearchParams, require: (id) => {
    if (mocks[id]) return mocks[id];
    if (id.startsWith("@/")) { const target = id.slice(2); return load(target + (fs.existsSync(path.join(root, target + ".tsx")) ? ".tsx" : ".ts")); }
    return local(id);
  } });
  cache.set(file, mod.exports); return mod.exports;
}

/** SSR renders the actual shared Markdown components without opening a mailbox or loading images. */
function render(text, range) {
  const { NoteMarkdown } = load("components/notes/note-markdown.tsx");
  // Source offsets are asserted separately; strip them so markup assertions stay readable.
  return renderToStaticMarkup(React.createElement(NoteMarkdown, { vaultId: "v", noteId: "n", text, range, onLink: () => {} })).replace(/ data-note-(?:from|to)="\d+"/g, "");
}

test("reader and live table blocks share GFM rendering, emphasis and BR conversion", () => {
  const text = "# Intro\n\n| Command | Description |\n| --- | --- |\n| /Shutdown | **Stop**<br>停止 |\n\nLast paragraph\n";
  const from = text.indexOf("| Command"); const to = text.indexOf("Last paragraph");
  const live = render(text, { from, to });
  const { NotePreview } = load("components/notes/note-preview.tsx");
  const reading = renderToStaticMarkup(React.createElement(NotePreview, { vaultId: "v", noteId: "n", text, onLink: () => {} })).replace(/ data-note-(?:from|to)="\d+"/g, "");
  assert.match(live, /<table>/); assert.match(live, /<strong>Stop<\/strong><br\/>\s*停止/);
  assert.ok(reading.includes(live)); assert.doesNotMatch(live, /Intro|Last paragraph/);
});

test("lists, quotes, strike-through and code use the same shared renderer", () => {
  const html = render("> **Quote**\n\n- one\n  - nested\n\n~~deleted~~\n\n```html\n<br>\n<script>alert(1)</script>\n```\n");
  assert.match(html, /<blockquote>/); assert.match(html, /<ul>/); assert.match(html, /<del>deleted<\/del>/);
  assert.match(html, /&lt;br&gt;/); assert.match(html, /&lt;script&gt;/); assert.doesNotMatch(html, /<script>/);
});

test("live blocks retain full-document link definitions", () => {
  const text = "Intro\n\n[reference][target]\n\n[target]: https://example.com/path\n";
  const html = render(text, { from: text.indexOf("[reference]"), to: text.indexOf("[target]:") });
  assert.match(html, /href="https:\/\/example.com\/path"/); assert.doesNotMatch(html, /Intro/);
});

test("remote images load by default with no-referrer and a delegated preview target", () => {
  const html = render("![remote](https://example.com/a.png)\n\n![local](assets/a.png)");
  assert.match(html, /src="https:\/\/example.com\/a.png"/);
  assert.match(html, /data-note-remote="https:\/\/example.com\/a.png"/);
  assert.match(html, /referrerPolicy="no-referrer"/);
  assert.match(html, /data-note-path="assets\/a.png"/);
  assert.doesNotMatch(html, /点击加载远程图片/);
});

test("leading BR preserves following prose without enabling HTML", () => {
  const text = "<br>\n这行必须显示\n\n下一段";
  const html = render(text);
  const live = render(text, { from: 0, to: text.indexOf("下一段") });
  assert.match(live, /这行必须显示/);
  assert.match(html, /这行必须显示/); assert.match(html, /下一段/); assert.match(html, /<br\/>/);
});

test("nested link definitions retain first-definition precedence across live blocks", () => {
  const text = "[打开][r]\n\n> [r]: A.md\n\n[r]: B.md\n\n末尾";
  const live = render(text, { from: 0, to: text.indexOf(">") });
  assert.match(live, /href="A.md"/); assert.doesNotMatch(live, /href="B.md"/);
});

test("footnotes keep global numbering and unique IDs across live blocks", () => {
  const text = "甲[^a]\n\n乙[^b]，再次引用[^a]\n\n[^a]: 注A\n[^b]: 注B\n\n末尾";
  const first = render(text, { from: 0, to: text.indexOf("乙") });
  const second = render(text, { from: text.indexOf("乙"), to: text.indexOf("[^a]:") });
  const footnotes = render(text, { from: text.length, to: text.length, footnotesOnly: true });
  assert.match(first, /href="#user-content-fn-a"[^>]*>1</);
  assert.match(second, /href="#user-content-fn-b"[^>]*>2</);
  assert.match(second, /href="#user-content-fn-a"[^>]*>1</);
  assert.doesNotMatch(first + second, /data-footnotes=/);
  assert.match(footnotes, /data-footnotes=/);
  const ids = [...(first + second + footnotes).matchAll(/\sid="([^"]+)"/g)].map((match) => match[1]);
  assert.equal(new Set(ids).size, ids.length);
});

test("range conversion preserves wiki embeds following frontmatter without duplicating neighbors", () => {
  const text = "---\ntitle: Example\n---\n# Intro\n\n![[assets/中文 图片.png]]\n\nTail\n";
  const html = render(text, { from: text.indexOf("![["), to: text.indexOf("Tail") });
  assert.match(html, /<img/); assert.doesNotMatch(html, /<h1>|<summary>|Tail/);
  assert.match(html, /data-note-path=/);
});

test("elements carry source offsets that survive frontmatter and wiki rewrites", () => {
  const { NoteMarkdown } = load("components/notes/note-markdown.tsx");
  const text = "---\ntitle: x\n---\n[[Target]] and **bold** tail\n\n- [ ] task\n";
  const html = renderToStaticMarkup(React.createElement(NoteMarkdown, { vaultId: "v", noteId: "n", text, onLink: () => {}, onTask: () => {} }));
  assert.match(html, new RegExp(`<strong data-note-from="${text.indexOf("**bold")}" data-note-to="${text.indexOf(" tail")}"`));
  assert.match(html, new RegExp(`<li class="task-list-item" data-note-from="${text.indexOf("- [ ]")}"`));
  assert.doesNotMatch(html, /<input[^>]* disabled=/);
});

test("the same note is parsed once for every live block", () => {
  const { parseNote } = load("lib/notes/parse-note.ts");
  const text = "# A\n\nB\n\nC\n";
  const first = parseNote(text); assert.ok(first === parseNote(text));
});
